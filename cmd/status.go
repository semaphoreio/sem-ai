package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/gitutil"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var (
	statusProjectFlag string
	statusBranchFlag  string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick CI status for a branch or PR",
	Long:  "Compound command: finds workflows for the current branch/PR, shows pipeline and job status summary.",
	Example: `  sem-agent status
  sem-agent status --branch main
  sem-agent status --project my-project --branch feature-x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		// Resolve project
		project := statusProjectFlag
		if project == "" {
			p, err := detectProject()
			if err != nil {
				output.Error("context_error", "could not detect project from git remote — use --project", 1)
				return err
			}
			project = p
		}

		projectID, err := resolveProjectID(project)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		// Resolve branch
		branch := statusBranchFlag
		if branch == "" {
			b, err := gitutil.CurrentBranch()
			if err == nil {
				branch = b
			}
		}

		// Fetch workflows
		params := url.Values{}
		params.Set("project_id", projectID)
		if branch != "" {
			params.Set("branch_name", branch)
		}

		c := client.New()
		resp, err := c.ListWithParams("plumber-workflows", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d", resp.StatusCode), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var workflows []struct {
			WfID         string `json:"wf_id"`
			BranchName   string `json:"branch_name"`
			CommitSHA    string `json:"commit_sha"`
			InitialPplID string `json:"initial_ppl_id"`
		}
		if err := json.Unmarshal(resp.Body, &workflows); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		if len(workflows) == 0 {
			output.Result(map[string]any{
				"project": project,
				"branch":  branch,
				"status":  "no_workflows",
				"message": "no workflows found for this branch",
			})
			return nil
		}

		// Get latest workflow's pipeline (detailed=true to include blocks)
		latest := workflows[0]
		pplParams := url.Values{}
		pplParams.Set("detailed", "true")
		pplResp, err := c.ListWithParams("pipelines/"+latest.InitialPplID, pplParams)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var pplData struct {
			Pipeline struct {
				PplID        string `json:"ppl_id"`
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
				Jobs   []struct {
					Name  string `json:"name"`
					JobID string `json:"job_id"`
				} `json:"jobs"`
			} `json:"blocks"`
		}

		if pplResp.StatusCode == 200 {
			_ = json.Unmarshal(pplResp.Body, &pplData)
		}

		// Build status summary
		type blockStatus struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Result string `json:"result,omitempty"`
		}

		blocks := make([]blockStatus, 0)
		for _, b := range pplData.Blocks {
			blocks = append(blocks, blockStatus{
				Name:   b.Name,
				State:  b.State,
				Result: b.Result,
			})
		}

		status := map[string]any{
			"project":     project,
			"branch":      latest.BranchName,
			"commit_sha":  latest.CommitSHA,
			"workflow_id":  latest.WfID,
			"pipeline_id":  latest.InitialPplID,
			"pipeline": map[string]any{
				"name":          pplData.Pipeline.Name,
				"state":         pplData.Pipeline.State,
				"result":        pplData.Pipeline.Result,
				"result_reason": pplData.Pipeline.ResultReason,
				"created_at":    pplData.Pipeline.CreatedAt,
				"done_at":       pplData.Pipeline.DoneAt,
			},
			"blocks":           blocks,
			"total_workflows":  len(workflows),
		}

		output.Result(status)
		return nil
	},
}

// detectProject tries to find the Semaphore project name from git remote.
func detectProject() (string, error) {
	remoteURL, err := gitutil.RemoteURL("origin")
	if err != nil {
		return "", err
	}

	repoName := gitutil.RepoName(remoteURL)
	if repoName == "" {
		return "", fmt.Errorf("could not extract repo name from %q", remoteURL)
	}

	// Try matching against Semaphore projects
	c := client.New()
	resp, err := c.List("projects")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return repoName, nil // fallback to repo name
	}

	var projects []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Repository struct {
				URL  string `json:"url"`
				Name string `json:"name"`
			} `json:"repository"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(resp.Body, &projects); err != nil {
		return repoName, nil
	}

	// Match by repo URL or repo name
	ownerRepo := gitutil.RepoOwnerAndName(remoteURL)
	for _, p := range projects {
		pOwnerRepo := gitutil.RepoOwnerAndName(p.Spec.Repository.URL)
		if pOwnerRepo == ownerRepo {
			return p.Metadata.Name, nil
		}
		if p.Spec.Repository.Name == repoName {
			return p.Metadata.Name, nil
		}
	}

	return repoName, nil
}

func init() {
	statusCmd.Flags().StringVar(&statusProjectFlag, "project", "", "project name (auto-detected from git remote if omitted)")
	statusCmd.Flags().StringVar(&statusBranchFlag, "branch", "", "branch name (auto-detected from HEAD if omitted)")
	rootCmd.AddCommand(statusCmd)
}
