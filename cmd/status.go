package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/gitutil"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var (
	statusProjectFlag  string
	statusBranchFlag   string
	statusPRFlag       string
	statusExitCodeFlag bool
)

// Process exit codes for `status --exit-code`, so poll loops can branch on them.
// 0 pass · 8 pending · 1 fail · 2 ambiguous (multi-project) · 3 no workflow / undetected.
const (
	exitPass       = 0
	exitFail       = 1
	exitAmbiguous  = 2
	exitNoWorkflow = 3
	exitPending    = 8
)

// statusExitCode is set by the status command when --exit-code is requested and
// read by the CLI Execute() wrapper (cmd/root.go), which is the ONLY place that
// may call os.Exit. The MCP server runs commands in-process via executeCobra,
// which calls rootCmd.Execute() directly and never routes through Execute() —
// so this never terminates the long-lived MCP process.
var statusExitCode int

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick CI status for the current branch, a PR, or a project",
	Long: "Compound command: finds the CI workflow for the current commit/branch (or a PR), " +
		"shows the pipeline and block status. Project and branch are auto-detected from the git " +
		"remote and HEAD when not given. With --exit-code, returns a poll-friendly exit code.",
	Example: `  sem-ai status                      # current repo, current branch, current commit
  sem-ai status --branch main
  sem-ai status --pr 422
  sem-ai status --project my-project --branch feature-x
  until sem-ai status --exit-code; do sleep 20; done   # watch to green`,
	RunE: func(cmd *cobra.Command, args []string) error {
		statusExitCode = 0
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}

		// Resolve candidate projects. An explicit --project pins exactly one;
		// otherwise the git remote may match several Semaphore projects — we
		// keep all of them and let the workflow lookup disambiguate.
		var candidates []projectCandidate
		if statusProjectFlag != "" {
			id, err := resolveProjectID(statusProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			candidates = []projectCandidate{{Name: statusProjectFlag, ID: id}}
		} else {
			cands, err := detectProjectCandidates()
			if err != nil {
				if statusExitCodeFlag {
					statusExitCode = exitNoWorkflow
					return nil
				}
				output.Error("context_error", err.Error()+"; use --project", 1)
				return err
			}
			candidates = cands
		}

		// Resolve the selector: a PR number (label) takes precedence over branch.
		branch := statusBranchFlag
		if branch == "" && statusPRFlag == "" {
			if b, err := gitutil.CurrentBranch(); err == nil {
				branch = b
			}
		}
		wantSHA := ""
		if statusPRFlag == "" {
			if sha, err := gitutil.CurrentCommitSHA(); err == nil {
				wantSHA = sha
			}
		}

		c := client.New()
		var found []map[string]any
		for _, cand := range candidates {
			st, ok, err := fetchProjectStatus(c, cand, branch, statusPRFlag, wantSHA)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if ok {
				found = append(found, st)
			}
		}

		switch len(found) {
		case 0:
			selector := branch
			if statusPRFlag != "" {
				selector = "PR #" + statusPRFlag
			}
			if statusExitCodeFlag {
				statusExitCode = exitNoWorkflow
				return nil
			}
			output.Result(map[string]any{
				"status":  "no_workflows",
				"branch":  branch,
				"message": fmt.Sprintf("no workflow found for %q in the detected project(s)", selector),
			})
			return nil
		case 1:
			st := found[0]
			if statusExitCodeFlag {
				statusExitCode = exitCodeForBucket(st["result_bucket"])
			}
			output.Result(st)
			return nil
		default:
			// The repo maps to several Semaphore projects that each ran this
			// commit/branch — never guess which one "is" the build.
			if statusExitCodeFlag {
				statusExitCode = exitAmbiguous
			}
			names := make([]string, len(found))
			for i, st := range found {
				names[i], _ = st["project"].(string)
			}
			output.Result(map[string]any{
				"multiple_projects": true,
				"message": fmt.Sprintf("this repo maps to %d Semaphore projects (%s) that ran this commit; pass --project to pick one",
					len(found), strings.Join(names, ", ")),
				"projects": found,
			})
			return nil
		}
	},
}

// fetchProjectStatus looks up the workflow for the given selector in one project
// and, if found, returns its pipeline status summary. ok=false means the project
// has no matching workflow (so it is not part of the answer).
func fetchProjectStatus(c *client.Client, proj projectCandidate, branch, pr, wantSHA string) (map[string]any, bool, error) {
	params := url.Values{}
	params.Set("project_id", proj.ID)
	switch {
	case pr != "":
		params.Set("label", pr)
	case branch != "":
		params.Set("branch_name", branch)
	}

	resp, err := c.ListWithParams("plumber-workflows", params)
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("workflows: HTTP %d", resp.StatusCode)
	}

	var workflows []struct {
		WfID         string `json:"wf_id"`
		BranchName   string `json:"branch_name"`
		CommitSHA    string `json:"commit_sha"`
		InitialPplID string `json:"initial_ppl_id"`
	}
	if err := json.Unmarshal(resp.Body, &workflows); err != nil {
		return nil, false, err
	}
	if len(workflows) == 0 {
		return nil, false, nil
	}

	// Prefer the workflow for the exact HEAD commit; the API returns newest-first,
	// so workflows[0] is the latest run on the branch as a fallback.
	chosen := workflows[0]
	matchedBy := "latest_on_branch"
	if wantSHA != "" {
		for _, w := range workflows {
			if w.CommitSHA == wantSHA || strings.HasPrefix(w.CommitSHA, wantSHA) || strings.HasPrefix(wantSHA, w.CommitSHA) {
				chosen = w
				matchedBy = "commit_sha"
				break
			}
		}
	}

	pplParams := url.Values{}
	pplParams.Set("detailed", "true")
	pplResp, err := c.ListWithParams("pipelines/"+chosen.InitialPplID, pplParams)
	if err != nil {
		return nil, false, err
	}

	var pplData struct {
		Pipeline struct {
			Name         string `json:"name"`
			State        string `json:"state"`
			Result       string `json:"result"`
			ResultReason string `json:"result_reason"`
			CreatedAt    string `json:"created_at"`
			DoneAt       string `json:"done_at"`
		} `json:"pipeline"`
		Blocks []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Result string `json:"result"`
		} `json:"blocks"`
	}
	if pplResp.StatusCode == 200 {
		_ = json.Unmarshal(pplResp.Body, &pplData)
	}

	type blockStatus struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Result string `json:"result,omitempty"`
	}
	blocks := make([]blockStatus, 0, len(pplData.Blocks))
	for _, b := range pplData.Blocks {
		blocks = append(blocks, blockStatus{Name: b.Name, State: b.State, Result: b.Result})
	}

	st := map[string]any{
		"project":         proj.Name,
		"branch":          chosen.BranchName,
		"commit_sha":      chosen.CommitSHA,
		"matched_by":      matchedBy,
		"workflow_id":     chosen.WfID,
		"pipeline_id":     chosen.InitialPplID,
		"total_workflows": len(workflows),
		"pipeline": map[string]any{
			"name":          pplData.Pipeline.Name,
			"state":         pplData.Pipeline.State,
			"result":        pplData.Pipeline.Result,
			"result_reason": pplData.Pipeline.ResultReason,
			"created_at":    pplData.Pipeline.CreatedAt,
			"done_at":       pplData.Pipeline.DoneAt,
		},
		"result_bucket": bucketForPipeline(pplData.Pipeline.State, pplData.Pipeline.Result),
	}
	return st, true, nil
}

// bucketForPipeline collapses Semaphore's state/result into one of
// passed / failed / pending, for both display and exit-code mapping.
func bucketForPipeline(state, result string) string {
	if state != "" && state != "done" {
		return "pending"
	}
	if result == "passed" {
		return "passed"
	}
	if result == "" {
		return "pending"
	}
	return "failed"
}

func exitCodeForBucket(bucket any) int {
	switch bucket {
	case "passed":
		return exitPass
	case "pending":
		return exitPending
	default:
		return exitFail
	}
}

type projectCandidate struct {
	Name string
	ID   string
}

// detectProjectCandidates returns every Semaphore project whose repository
// matches the current git remote ("origin"). It returns an empty slice when
// nothing matches. Owner/repo is the stronger signal; a bare repo-name match is
// only used when no owner/repo match exists.
func detectProjectCandidates() ([]projectCandidate, error) {
	remoteURL, err := gitutil.RemoteURL("origin")
	if err != nil {
		return nil, err
	}
	ownerRepo := gitutil.RepoOwnerAndName(remoteURL)
	repoName := gitutil.RepoName(remoteURL)
	if ownerRepo == "" && repoName == "" {
		return nil, fmt.Errorf("could not extract a repo name from remote %q", remoteURL)
	}

	c := client.New()
	resp, err := c.List("projects")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("projects list returned HTTP %d", resp.StatusCode)
	}

	var projects []struct {
		Metadata struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"metadata"`
		Spec struct {
			Repository struct {
				URL  string `json:"url"`
				Name string `json:"name"`
			} `json:"repository"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(resp.Body, &projects); err != nil {
		return nil, err
	}

	var byOwnerRepo, byName []projectCandidate
	for _, p := range projects {
		cand := projectCandidate{Name: p.Metadata.Name, ID: p.Metadata.ID}
		if ownerRepo != "" && gitutil.RepoOwnerAndName(p.Spec.Repository.URL) == ownerRepo {
			byOwnerRepo = append(byOwnerRepo, cand)
		} else if repoName != "" && p.Spec.Repository.Name == repoName {
			byName = append(byName, cand)
		}
	}
	if len(byOwnerRepo) > 0 {
		return byOwnerRepo, nil
	}
	return byName, nil
}

// detectProject returns the single Semaphore project for the current git remote.
// It errors when the remote matches no project, or matches several (caller must
// then pass --project) — it never guesses one of multiple matches.
func detectProject() (string, error) {
	cands, err := detectProjectCandidates()
	if err != nil {
		return "", err
	}
	switch len(cands) {
	case 0:
		return "", fmt.Errorf("could not detect a Semaphore project from git remote 'origin'")
	case 1:
		return cands[0].Name, nil
	default:
		names := make([]string, len(cands))
		for i, c := range cands {
			names[i] = c.Name
		}
		return "", fmt.Errorf("git remote 'origin' maps to %d Semaphore projects (%s)", len(cands), strings.Join(names, ", "))
	}
}

func init() {
	statusCmd.Flags().StringVar(&statusProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	statusCmd.Flags().StringVar(&statusBranchFlag, "branch", "", "branch name (auto-detected from HEAD if omitted)")
	statusCmd.Flags().StringVar(&statusPRFlag, "pr", "", "pull-request number (overrides --branch; matches the PR's workflow)")
	statusCmd.Flags().BoolVar(&statusExitCodeFlag, "exit-code", false, "exit 0=pass 8=pending 1=fail 2=ambiguous 3=none (for poll loops)")
	rootCmd.AddCommand(statusCmd)
}
