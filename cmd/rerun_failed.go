package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var rerunFailedCmd = &cobra.Command{
	Use:   "rerun-failed <pipeline-id>",
	Short: "Rebuild only failed blocks in a pipeline (partial rebuild)",
	Long: `Triggers a partial rebuild for failed blocks only. Returns the new pipeline ID.
Use 'sem-ai watch <workflow-id>' to follow progress after.`,
	Args: cobra.ExactArgs(1),
	Example: `  sem-ai rerun-failed <pipeline-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		pipelineID := args[0]
		c := client.New()

		// 1. Check current pipeline state
		params := url.Values{}
		params.Set("detailed", "true")
		resp, err := c.ListWithParams("pipelines/"+pipelineID, params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d", resp.StatusCode), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var pplData struct {
			Pipeline struct {
				Result string `json:"result"`
				WfID   string `json:"wf_id"`
			} `json:"pipeline"`
			Blocks []struct {
				Name   string `json:"name"`
				Result string `json:"result"`
			} `json:"blocks"`
		}
		json.Unmarshal(resp.Body, &pplData)

		failedBlocks := make([]string, 0)
		for _, b := range pplData.Blocks {
			if b.Result == "failed" || b.Result == "stopped" {
				failedBlocks = append(failedBlocks, b.Name)
			}
		}

		if len(failedBlocks) == 0 {
			output.Result(map[string]any{
				"pipeline_id":   pipelineID,
				"status":        "no_failures",
				"message":       "no failed blocks to rebuild",
				"pipeline_result": pplData.Pipeline.Result,
			})
			return nil
		}

		// 2. Trigger partial rebuild
		token := client.NewRequestToken()
		action := fmt.Sprintf("partial_rebuild?request_token=%s", token)
		rebuildResp, err := c.PostAction("pipelines", pipelineID, action, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if rebuildResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", rebuildResp.StatusCode, string(rebuildResp.Body)), rebuildResp.StatusCode)
			return fmt.Errorf("rebuild failed")
		}

		output.Result(map[string]any{
			"pipeline_id":   pipelineID,
			"workflow_id":   pplData.Pipeline.WfID,
			"status":        "rebuild_triggered",
			"failed_blocks": failedBlocks,
			"message":       fmt.Sprintf("rebuilding %d failed blocks — use 'sem-ai watch %s' to follow", len(failedBlocks), pplData.Pipeline.WfID),
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rerunFailedCmd)
}
