package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/semaphoreio/agent-cli/pkg/testparse"
	"github.com/spf13/cobra"
)

var (
	diagnoseProjectFlag string
	diagnoseBranchFlag  string
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose [workflow-id]",
	Short: "Failure diagnosis — one command, full root cause",
	Long: `Compound command that composes: workflow → pipeline → failed blocks → failed jobs →
logs → test results into a single structured diagnosis.

If no workflow ID is given, finds the latest workflow for the current project/branch.`,
	Args: cobra.MaximumNArgs(1),
	Example: `  sem-agent diagnose
  sem-agent diagnose <workflow-id>
  sem-agent diagnose --project my-project --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		c := client.New()
		var wfID, pipelineID string

		if len(args) == 1 {
			wfID = args[0]
			// Get workflow to find pipeline
			wfResp, err := c.Get("plumber-workflows", wfID)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if wfResp.StatusCode != 200 {
				output.Error("api_error", fmt.Sprintf("HTTP %d", wfResp.StatusCode), wfResp.StatusCode)
				return fmt.Errorf("workflow not found")
			}
			var wf struct {
				Workflow struct {
					InitialPplID string `json:"initial_ppl_id"`
				} `json:"workflow"`
			}
			json.Unmarshal(wfResp.Body, &wf)
			pipelineID = wf.Workflow.InitialPplID
		} else {
			// Auto-detect from project/branch
			project := diagnoseProjectFlag
			if project == "" {
				p, err := detectProject()
				if err != nil {
					output.Error("context_error", "could not detect project — use --project or pass workflow ID", 1)
					return err
				}
				project = p
			}
			projectID, err := resolveProjectID(project)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}

			branch := diagnoseBranchFlag
			params := url.Values{}
			params.Set("project_id", projectID)
			if branch != "" {
				params.Set("branch_name", branch)
			}

			resp, err := c.ListWithParams("plumber-workflows", params)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			var wfs []struct {
				WfID         string `json:"wf_id"`
				InitialPplID string `json:"initial_ppl_id"`
				BranchName   string `json:"branch_name"`
			}
			if resp.StatusCode == 200 {
				json.Unmarshal(resp.Body, &wfs)
			}
			if len(wfs) == 0 {
				output.Error("not_found", "no workflows found", 404)
				return fmt.Errorf("no workflows found")
			}
			wfID = wfs[0].WfID
			pipelineID = wfs[0].InitialPplID
		}

		// Get pipeline details
		params := url.Values{}
		params.Set("detailed", "true")
		pplResp, err := c.ListWithParams("pipelines/"+pipelineID, params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if pplResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d", pplResp.StatusCode), pplResp.StatusCode)
			return fmt.Errorf("pipeline fetch failed")
		}

		var pplData struct {
			Pipeline struct {
				PplID         string `json:"ppl_id"`
				Name          string `json:"name"`
				State         string `json:"state"`
				Result        string `json:"result"`
				ResultReason  string `json:"result_reason"`
				Error         string `json:"error_description"`
				BranchName    string `json:"branch_name"`
				CommitSHA     string `json:"commit_sha"`
				CommitMessage string `json:"commit_message"`
				CreatedAt     string `json:"created_at"`
				DoneAt        string `json:"done_at"`
			} `json:"pipeline"`
			Blocks []struct {
				Name   string `json:"name"`
				State  string `json:"state"`
				Result string `json:"result"`
				Error  string `json:"error_description"`
				Jobs   []struct {
					Name   string `json:"name"`
					JobID  string `json:"job_id"`
					Status string `json:"status"`
					Result string `json:"result"`
				} `json:"jobs"`
			} `json:"blocks"`
		}
		json.Unmarshal(pplResp.Body, &pplData)

		// Find failed blocks and jobs
		type failedJob struct {
			Block     string              `json:"block"`
			JobName   string              `json:"job_name"`
			JobID     string              `json:"job_id"`
			LogTail   string              `json:"log_tail,omitempty"`
			TestReport *testparse.TestReport `json:"test_report,omitempty"`
		}

		failedJobs := make([]failedJob, 0)
		failedBlocks := make([]string, 0)

		for _, block := range pplData.Blocks {
			if block.Result != "passed" && block.Result != "" {
				failedBlocks = append(failedBlocks, block.Name)
			}
			for _, job := range block.Jobs {
				if job.Result == "FAILED" || job.Result == "STOPPED" {
					fj := failedJob{
						Block:   block.Name,
						JobName: job.Name,
						JobID:   job.JobID,
					}

					// Fetch log tail for failed job
					logResp, err := c.Get("logs", job.JobID)
					if err == nil && logResp.StatusCode == 200 {
						var logs struct {
							Events []struct {
								Output    string `json:"output"`
								Directive string `json:"directive"`
								Type      string `json:"event"`
								ExitCode  int    `json:"exit_code"`
							} `json:"events"`
						}
						if json.Unmarshal(logResp.Body, &logs) == nil {
							// Get last N lines of output + failed commands
							var allOutput strings.Builder
							var failedCmds []string
							for _, e := range logs.Events {
								if e.Output != "" {
									allOutput.WriteString(e.Output)
								}
								if e.ExitCode != 0 && e.Directive != "" {
									failedCmds = append(failedCmds, fmt.Sprintf("$ %s (exit %d)", e.Directive, e.ExitCode))
								}
							}

							// Tail: last 500 chars
							full := allOutput.String()
							if len(full) > 500 {
								full = full[len(full)-500:]
							}
							fj.LogTail = strings.TrimSpace(full)
							if len(failedCmds) > 0 {
								fj.LogTail = strings.Join(failedCmds, "\n") + "\n---\n" + fj.LogTail
							}

							// Parse test results
							fj.TestReport = testparse.ParseFromLogs(allOutput.String())
						}
					}

					failedJobs = append(failedJobs, fj)
				}
			}
		}

		// Build diagnosis
		ppl := pplData.Pipeline
		verdict := "passed"
		if ppl.Result != "passed" {
			verdict = ppl.Result
		}

		diagnosis := map[string]any{
			"workflow_id": wfID,
			"pipeline": map[string]any{
				"id":             ppl.PplID,
				"name":           ppl.Name,
				"state":          ppl.State,
				"result":         ppl.Result,
				"result_reason":  ppl.ResultReason,
				"error":          ppl.Error,
				"branch":         ppl.BranchName,
				"commit_sha":     ppl.CommitSHA,
				"commit_message": ppl.CommitMessage,
				"created_at":     ppl.CreatedAt,
				"done_at":        ppl.DoneAt,
			},
			"verdict":        verdict,
			"failed_blocks":  failedBlocks,
			"failed_jobs":    failedJobs,
			"total_blocks":   len(pplData.Blocks),
		}

		output.Result(diagnosis)
		return nil
	},
}

func init() {
	diagnoseCmd.Flags().StringVar(&diagnoseProjectFlag, "project", "", "project name (auto-detected from git if omitted)")
	diagnoseCmd.Flags().StringVar(&diagnoseBranchFlag, "branch", "", "branch name")
	rootCmd.AddCommand(diagnoseCmd)
}
