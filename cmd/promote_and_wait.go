package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var (
	pawTargetFlag   string
	pawConfirmFlag  bool
	pawOverrideFlag bool
	pawIntervalFlag string
)

var promoteAndWaitCmd = &cobra.Command{
	Use:   "promote-and-wait <pipeline-id>",
	Short: "Promote and wait for the promoted pipeline to finish",
	Long: `Compound command: triggers promotion → finds the promoted pipeline → polls until done →
returns final result. Essential for agents that deploy then verify.`,
	Args: cobra.ExactArgs(1),
	Example: `  # Dry run
  sem-ai promote-and-wait <pipeline-id> --target "Staging Deploy"

  # Execute and wait
  sem-ai promote-and-wait <pipeline-id> --target "Staging Deploy" --confirm`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		pipelineID := args[0]

		if pawTargetFlag == "" {
			output.Error("invalid_args", "--target is required", 1)
			return fmt.Errorf("--target is required")
		}

		if !pawConfirmFlag {
			output.Result(map[string]any{
				"action":      "promote-and-wait",
				"pipeline_id": pipelineID,
				"target":      pawTargetFlag,
				"dry_run":     true,
				"message":     "add --confirm to execute promotion and wait for completion",
			})
			return nil
		}

		interval, err := time.ParseDuration(pawIntervalFlag)
		if err != nil || interval < 5*time.Second {
			interval = 15 * time.Second
		}

		c := client.New()

		// 1. Get current promotions to know baseline
		preParams := url.Values{}
		preParams.Set("pipeline_id", pipelineID)
		preResp, _ := c.ListWithParams("promotions", preParams)
		var prePromotions []struct {
			ScheduledPipelineID string `json:"scheduled_pipeline_id"`
		}
		if preResp != nil && preResp.StatusCode == 200 {
			json.Unmarshal(preResp.Body, &prePromotions)
		}
		existingPplIDs := make(map[string]bool)
		for _, p := range prePromotions {
			existingPplIDs[p.ScheduledPipelineID] = true
		}

		// 2. Trigger promotion
		reqBody := map[string]any{
			"pipeline_id": pipelineID,
			"name":        pawTargetFlag,
			"override":    pawOverrideFlag,
		}
		bodyBytes, _ := json.Marshal(reqBody)
		promResp, err := c.Post("promotions", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if promResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", promResp.StatusCode, string(promResp.Body)), promResp.StatusCode)
			return fmt.Errorf("promotion failed")
		}

		// 3. Find the new promoted pipeline
		var promotedPplID string
		for attempt := 0; attempt < 10; attempt++ {
			postResp, _ := c.ListWithParams("promotions", preParams)
			if postResp != nil && postResp.StatusCode == 200 {
				var postPromotions []struct {
					Name                string `json:"name"`
					ScheduledPipelineID string `json:"scheduled_pipeline_id"`
				}
				json.Unmarshal(postResp.Body, &postPromotions)
				for _, p := range postPromotions {
					if !existingPplIDs[p.ScheduledPipelineID] && p.Name == pawTargetFlag {
						promotedPplID = p.ScheduledPipelineID
						break
					}
				}
			}
			if promotedPplID != "" {
				break
			}
			time.Sleep(2 * time.Second)
		}

		if promotedPplID == "" {
			output.Result(map[string]any{
				"status":      "promoted",
				"pipeline_id": pipelineID,
				"target":      pawTargetFlag,
				"message":     "promotion triggered but could not find promoted pipeline ID to watch",
			})
			return nil
		}

		// 4. Poll until promoted pipeline is done
		start := time.Now()
		for {
			pplParams := url.Values{}
			pplParams.Set("detailed", "true")
			pplResp, err := c.ListWithParams("pipelines/"+promotedPplID, pplParams)
			if err == nil && pplResp.StatusCode == 200 {
				var ppl struct {
					Pipeline struct {
						State  string `json:"state"`
						Result string `json:"result"`
						Name   string `json:"name"`
					} `json:"pipeline"`
				}
				json.Unmarshal(pplResp.Body, &ppl)
				if ppl.Pipeline.State == "done" || ppl.Pipeline.State == "DONE" {
					output.Result(map[string]any{
						"status":              "done",
						"source_pipeline_id":  pipelineID,
						"promoted_pipeline_id": promotedPplID,
						"target":              pawTargetFlag,
						"result":              ppl.Pipeline.Result,
						"elapsed":             time.Since(start).Round(time.Second).String(),
					})
					return nil
				}
			}
			time.Sleep(interval)
		}
	},
}

func init() {
	promoteAndWaitCmd.Flags().StringVar(&pawTargetFlag, "target", "", "promotion target name (required)")
	promoteAndWaitCmd.Flags().BoolVar(&pawConfirmFlag, "confirm", false, "REQUIRED to execute")
	promoteAndWaitCmd.Flags().BoolVar(&pawOverrideFlag, "override", false, "override promotion conditions")
	promoteAndWaitCmd.Flags().StringVar(&pawIntervalFlag, "interval", "15s", "poll interval")
	rootCmd.AddCommand(promoteAndWaitCmd)
}
