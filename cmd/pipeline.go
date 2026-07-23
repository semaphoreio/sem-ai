package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var pipelineCmd = &cobra.Command{
	Use:   "pipeline",
	Short: "Pipeline operations",
}

var pipelineShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show pipeline with blocks and jobs tree",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai pipeline show abc123-def456
  sem-ai pipeline show abc123-def456 --format yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		params := url.Values{}
		params.Set("detailed", "true")
		resp, err := c.ListWithParams("pipelines/"+args[0], params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		// Parse raw pipeline response
		var raw struct {
			Pipeline json.RawMessage `json:"pipeline"`
			Blocks   json.RawMessage `json:"blocks"`
		}
		if err := json.Unmarshal(resp.Body, &raw); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		// Parse pipeline details
		var ppl struct {
			ID            string `json:"ppl_id"`
			Name          string `json:"name"`
			State         string `json:"state"`
			Result        string `json:"result"`
			ResultReason  string `json:"result_reason"`
			Error         string `json:"error_description"`
			BranchName    string `json:"branch_name"`
			CommitSHA     string `json:"commit_sha"`
			CommitMessage string `json:"commit_message"`
			YAMLFile      string `json:"yaml_file_name"`
			CreatedAt     string `json:"created_at"`
			DoneAt        string `json:"done_at"`
			WfID          string `json:"wf_id"`
			ProjectID     string `json:"project_id"`
		}
		if err := json.Unmarshal(raw.Pipeline, &ppl); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		// Parse blocks
		var blocks []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Result string `json:"result"`
			Jobs   []struct {
				Name  string `json:"name"`
				JobID string `json:"job_id"`
			} `json:"jobs"`
		}
		if raw.Blocks != nil {
			_ = json.Unmarshal(raw.Blocks, &blocks)
		}

		// Build structured output
		type jobOut struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		type blockOut struct {
			Name   string   `json:"name"`
			State  string   `json:"state"`
			Result string   `json:"result,omitempty"`
			Jobs   []jobOut `json:"jobs,omitempty"`
		}
		type pipelineOut struct {
			ID            string     `json:"id"`
			Name          string     `json:"name"`
			State         string     `json:"state"`
			Result        string     `json:"result,omitempty"`
			ResultReason  string     `json:"result_reason,omitempty"`
			Error         string     `json:"error,omitempty"`
			Branch        string     `json:"branch"`
			CommitSHA     string     `json:"commit_sha"`
			CommitMessage string     `json:"commit_message"`
			YAMLFile      string     `json:"yaml_file"`
			WorkflowID    string     `json:"workflow_id"`
			ProjectID     string     `json:"project_id"`
			CreatedAt     string     `json:"created_at"`
			DoneAt        string     `json:"done_at,omitempty"`
			Blocks        []blockOut `json:"blocks"`
		}

		out := pipelineOut{
			ID:            ppl.ID,
			Name:          ppl.Name,
			State:         ppl.State,
			Result:        ppl.Result,
			ResultReason:  ppl.ResultReason,
			Error:         ppl.Error,
			Branch:        ppl.BranchName,
			CommitSHA:     ppl.CommitSHA,
			CommitMessage: ppl.CommitMessage,
			YAMLFile:      ppl.YAMLFile,
			WorkflowID:    ppl.WfID,
			ProjectID:     ppl.ProjectID,
			CreatedAt:     ppl.CreatedAt,
			DoneAt:        ppl.DoneAt,
			Blocks:        make([]blockOut, 0),
		}

		for _, b := range blocks {
			bo := blockOut{
				Name:   b.Name,
				State:  b.State,
				Result: b.Result,
				Jobs:   make([]jobOut, 0),
			}
			for _, j := range b.Jobs {
				bo.Jobs = append(bo.Jobs, jobOut{Name: j.Name, ID: j.JobID})
			}
			out.Blocks = append(out.Blocks, bo)
		}

		output.Result(out)
		return nil
	},
}

var (
	pipelineListProjectFlag string
	pipelineListBranchFlag  string
	pipelineListDaysFlag    int
	pipelineListLimitFlag   int
	pipelineListFullFlag    bool
)

type pipelineSummary struct {
	ID            string `json:"id"`
	WorkflowID    string `json:"workflow_id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	Result        string `json:"result,omitempty"`
	ResultReason  string `json:"result_reason,omitempty"`
	Branch        string `json:"branch"`
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message"`
	YAMLFile      string `json:"yaml_file"`
	CreatedAt     string `json:"created_at,omitempty"`
	DoneAt        string `json:"done_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

func protoTimeString(seconds int64) string {
	if seconds == 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func summarizePipelines(items []json.RawMessage) []pipelineSummary {
	summaries := make([]pipelineSummary, 0, len(items))
	for _, raw := range items {
		var p struct {
			PplID         string `json:"ppl_id"`
			WfID          string `json:"wf_id"`
			Name          string `json:"name"`
			State         string `json:"state"`
			Result        string `json:"result"`
			ResultReason  string `json:"result_reason"`
			BranchName    string `json:"branch_name"`
			CommitSHA     string `json:"commit_sha"`
			CommitMessage string `json:"commit_message"`
			YAMLFile      string `json:"yaml_file_name"`
			Error         string `json:"error_description"`
			CreatedAt     struct {
				Seconds int64 `json:"seconds"`
			} `json:"created_at"`
			DoneAt struct {
				Seconds int64 `json:"seconds"`
			} `json:"done_at"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		summaries = append(summaries, pipelineSummary{
			ID:            p.PplID,
			WorkflowID:    p.WfID,
			Name:          p.Name,
			State:         p.State,
			Result:        p.Result,
			ResultReason:  p.ResultReason,
			Branch:        p.BranchName,
			CommitSHA:     p.CommitSHA,
			CommitMessage: firstLine(p.CommitMessage),
			YAMLFile:      p.YAMLFile,
			CreatedAt:     protoTimeString(p.CreatedAt.Seconds),
			DoneAt:        protoTimeString(p.DoneAt.Seconds),
			Error:         p.Error,
		})
	}
	return summaries
}

func listWindowParams(params url.Values, days, limit int, now time.Time) url.Values {
	if days > 0 {
		params.Set("created_after", fmt.Sprintf("%d", now.AddDate(0, 0, -days).Unix()))
	}
	if limit > 0 {
		params.Set("page_size", fmt.Sprintf("%d", limit))
	}
	return params
}

var pipelineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pipelines for a project (trimmed summaries; --full for raw payload)",
	Example: `  sem-ai pipeline list --project my-project
  sem-ai pipeline list --project my-project --branch main --days 7
  sem-ai pipeline list --project my-project --limit 10 --full`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(pipelineListProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		params := url.Values{}
		params.Set("project_id", projectID)
		if pipelineListBranchFlag != "" {
			params.Set("branch_name", pipelineListBranchFlag)
		}
		listWindowParams(params, pipelineListDaysFlag, pipelineListLimitFlag, time.Now())
		resp, err := c.ListWithParams("pipelines", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		if pipelineListFullFlag {
			var result any
			json.Unmarshal(resp.Body, &result)
			output.Result(result)
			return nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(resp.Body, &items); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}
		output.Result(summarizePipelines(items))
		return nil
	},
}

var pipelineStopCmd = &cobra.Command{
	Use:     "stop <id>",
	Short:   "Stop a running pipeline",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai pipeline stop abc123-def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		body := []byte(`{"terminate_request": true}`)
		resp, err := c.Patch("pipelines", args[0], body)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "stopping", "pipeline_id": args[0]})
		return nil
	},
}

var pipelineRebuildCmd = &cobra.Command{
	Use:   "rebuild <id>",
	Short: "Rebuild failed blocks in a pipeline (partial rebuild)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai pipeline rebuild abc123-def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		token := client.NewRequestToken()
		action := fmt.Sprintf("partial_rebuild?request_token=%s", token)
		resp, err := c.PostAction("pipelines", args[0], action, nil)
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
			output.Result(map[string]string{"status": "rebuild_triggered", "pipeline_id": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

var (
	promoteTargetFlag  string
	promoteConfirmFlag bool
	promoteOverrideFlag bool
	promoteParamsFlag  []string
)

var pipelinePromoteCmd = &cobra.Command{
	Use:   "promote <pipeline-id>",
	Short: "Trigger a promotion (deploy). Requires --target and --confirm",
	Long: `Triggers a promotion on a pipeline. This is a DANGEROUS operation that can
deploy code to staging or production environments.

Safety:
  --confirm is REQUIRED. Without it, the command shows what would happen but does not execute.
  This prevents accidental deployments by AI agents or scripts.`,
	Args: cobra.ExactArgs(1),
	Example: `  # Dry run: see what would happen
  sem-ai pipeline promote abc123 --target "Staging Deploy"

  # Actually trigger promotion
  sem-ai pipeline promote abc123 --target "Staging Deploy" --confirm

  # Override conditions (e.g. promote even if tests failed)
  sem-ai pipeline promote abc123 --target "Staging Deploy" --confirm --override

  # With parameters
  sem-ai pipeline promote abc123 --target "Production Deploy" --confirm --param version=1.2.3`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}

		pipelineID := args[0]

		if promoteTargetFlag == "" {
			// List available promotions instead
			c := client.New()
			params := url.Values{}
			params.Set("pipeline_id", pipelineID)
			resp, err := c.ListWithParams("promotions", params)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if resp.StatusCode == 200 {
				var result any
				json.Unmarshal(resp.Body, &result)
				output.Result(map[string]any{
					"pipeline_id":  pipelineID,
					"message":      "use --target to specify promotion name, --confirm to execute",
					"promotions":   result,
				})
				return nil
			}
			output.Error("api_error", fmt.Sprintf("HTTP %d", resp.StatusCode), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		if !promoteConfirmFlag {
			// Dry run — show what would happen
			output.Result(map[string]any{
				"action":      "promote",
				"pipeline_id": pipelineID,
				"target":      promoteTargetFlag,
				"override":    promoteOverrideFlag,
				"params":      promoteParamsFlag,
				"dry_run":     true,
				"message":     "add --confirm to execute this promotion",
			})
			return nil
		}

		// Build promotion request
		reqBody := map[string]any{
			"pipeline_id": pipelineID,
			"name":        promoteTargetFlag,
			"override":    promoteOverrideFlag,
		}

		// Promotion parameters go as top-level body keys: the v1alpha
		// /promotions endpoint maps every key except
		// pipeline_id/name/override/request_token/switch_id/user_id to a
		// promotion env var. A nested "parameters" array makes the server
		// encode a list as an env-var string value and 500.
		for _, p := range promoteParamsFlag {
			if k, v, ok := strings.Cut(p, "="); ok {
				reqBody[k] = v
			}
		}

		bodyBytes, _ := json.Marshal(reqBody)
		c := client.New()
		resp, err := c.Post("promotions", bodyBytes)
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
			output.Result(map[string]string{
				"status":      "promotion_triggered",
				"pipeline_id": pipelineID,
				"target":      promoteTargetFlag,
			})
			return nil
		}
		output.Result(result)
		return nil
	},
}

func init() {
	pipelinePromoteCmd.Flags().StringVar(&promoteTargetFlag, "target", "", "promotion target name (e.g. 'Staging Deploy')")
	pipelinePromoteCmd.Flags().BoolVar(&promoteConfirmFlag, "confirm", false, "REQUIRED to actually execute the promotion")
	pipelinePromoteCmd.Flags().BoolVar(&promoteOverrideFlag, "override", false, "override promotion conditions (e.g. promote despite failures)")
	pipelinePromoteCmd.Flags().StringArrayVar(&promoteParamsFlag, "param", nil, "promotion parameters as key=value pairs")

	pipelineListCmd.Flags().StringVar(&pipelineListProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	pipelineListCmd.Flags().StringVar(&pipelineListBranchFlag, "branch", "", "filter by branch name")
	pipelineListCmd.Flags().IntVar(&pipelineListDaysFlag, "days", 30, "only pipelines created in the last N days (0 = no time filter)")
	pipelineListCmd.Flags().IntVar(&pipelineListLimitFlag, "limit", 30, "max number of pipelines to return (0 = server default)")
	pipelineListCmd.Flags().BoolVar(&pipelineListFullFlag, "full", false, "return the full raw API payload instead of trimmed summaries")
	pipelineCmd.AddCommand(pipelineListCmd)
	pipelineCmd.AddCommand(pipelineShowCmd)
	pipelineCmd.AddCommand(pipelineStopCmd)
	pipelineCmd.AddCommand(pipelineRebuildCmd)
	pipelineCmd.AddCommand(pipelinePromoteCmd)
	rootCmd.AddCommand(pipelineCmd)
}
