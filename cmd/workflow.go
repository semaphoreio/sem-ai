package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Workflow operations",
}

var (
	wfProjectFlag string
	wfBranchFlag  string
)

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflows for a project",
	Example: `  sem-agent workflow list --project my-project
  sem-agent workflow list --project my-project --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		projectID, err := resolveProjectID(wfProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		params := url.Values{}
		params.Set("project_id", projectID)
		if wfBranchFlag != "" {
			params.Set("branch_name", wfBranchFlag)
		}

		c := client.New()
		resp, err := c.ListWithParams("plumber-workflows", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var workflows []json.RawMessage
		if err := json.Unmarshal(resp.Body, &workflows); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		type wfSummary struct {
			ID          string `json:"id"`
			Branch      string `json:"branch"`
			CommitSHA   string `json:"commit_sha"`
			PipelineID  string `json:"initial_pipeline_id"`
			CreatedAt   any    `json:"created_at"`
		}

		summaries := make([]wfSummary, 0, len(workflows))
		for _, raw := range workflows {
			var w struct {
				WfID         string `json:"wf_id"`
				BranchName   string `json:"branch_name"`
				CommitSHA    string `json:"commit_sha"`
				InitialPplID string `json:"initial_ppl_id"`
				CreatedAt    any    `json:"created_at"`
			}
			if err := json.Unmarshal(raw, &w); err != nil {
				continue
			}
			summaries = append(summaries, wfSummary{
				ID:         w.WfID,
				Branch:     w.BranchName,
				CommitSHA:  w.CommitSHA,
				PipelineID: w.InitialPplID,
				CreatedAt:  w.CreatedAt,
			})
		}
		output.Result(summaries)
		return nil
	},
}

var workflowShowCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show workflow details",
	Args:    cobra.ExactArgs(1),
	Example: "  sem-agent workflow show abc123-def456",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("plumber-workflows", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}
		output.Result(result)
		return nil
	},
}

// resolveProjectID converts a project name to its ID.
// Tries direct GET first, falls back to listing all projects and matching by name.
func resolveProjectID(nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", fmt.Errorf("--project is required")
	}

	c := client.New()

	// Try direct lookup by name (works on some Semaphore hosts)
	resp, err := c.Get("projects", nameOrID)
	if err == nil && resp.StatusCode == 200 {
		var p struct {
			Metadata struct {
				ID string `json:"id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(resp.Body, &p); err == nil && p.Metadata.ID != "" {
			return p.Metadata.ID, nil
		}
	}

	// Fallback: list all projects and find by name
	listResp, err := c.List("projects")
	if err != nil {
		return "", err
	}
	if listResp.StatusCode == 200 {
		var projects []struct {
			Metadata struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(listResp.Body, &projects); err == nil {
			for _, p := range projects {
				if p.Metadata.Name == nameOrID {
					return p.Metadata.ID, nil
				}
			}
		}
	}

	// Maybe it's already a UUID — return as-is
	return nameOrID, nil
}

var workflowRerunCmd = &cobra.Command{
	Use:   "rerun <id>",
	Short: "Rerun a workflow (reschedule)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent workflow rerun abc123-def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		token := client.NewRequestToken()
		action := fmt.Sprintf("reschedule?request_token=%s", token)
		resp, err := c.PostAction("plumber-workflows", args[0], action, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "rerun_triggered", "workflow_id": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

var workflowStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a running workflow",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent workflow stop abc123-def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		token := client.NewRequestToken()
		action := fmt.Sprintf("terminate?request_token=%s", token)
		resp, err := c.PostAction("plumber-workflows", args[0], action, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "stopped", "workflow_id": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

var (
	wfRunProjectFlag string
	wfRunBranchFlag  string
)

var workflowRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Trigger a new workflow run",
	Example: `  sem-agent workflow run --project my-project
  sem-agent workflow run --project my-project --branch feature-x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		projectID, err := resolveProjectID(wfRunProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		// Rerun the latest workflow for this project/branch
		branch := wfRunBranchFlag
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

		var workflows []struct {
			WfID string `json:"wf_id"`
		}
		if resp.StatusCode == 200 {
			_ = json.Unmarshal(resp.Body, &workflows)
		}

		if len(workflows) == 0 {
			output.Error("not_found", "no workflows found to rerun", 404)
			return fmt.Errorf("no workflows found")
		}

		// Rerun latest
		token := client.NewRequestToken()
		action := fmt.Sprintf("reschedule?request_token=%s", token)
		rerunResp, err := c.PostAction("plumber-workflows", workflows[0].WfID, action, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if rerunResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", rerunResp.StatusCode, string(rerunResp.Body)), rerunResp.StatusCode)
			return fmt.Errorf("API returned %d", rerunResp.StatusCode)
		}
		var result any
		if err := json.Unmarshal(rerunResp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "triggered", "rerun_of": workflows[0].WfID})
			return nil
		}
		output.Result(result)
		return nil
	},
}

func init() {
	workflowListCmd.Flags().StringVar(&wfProjectFlag, "project", "", "project name or ID (required)")
	workflowListCmd.Flags().StringVar(&wfBranchFlag, "branch", "", "filter by branch name")
	workflowRunCmd.Flags().StringVar(&wfRunProjectFlag, "project", "", "project name or ID (required)")
	workflowRunCmd.Flags().StringVar(&wfRunBranchFlag, "branch", "", "branch to run workflow on")
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowShowCmd)
	workflowCmd.AddCommand(workflowRerunCmd)
	workflowCmd.AddCommand(workflowStopCmd)
	workflowCmd.AddCommand(workflowRunCmd)
	rootCmd.AddCommand(workflowCmd)
}
