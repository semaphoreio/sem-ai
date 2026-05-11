package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var watchIntervalFlag string

var watchCmd = &cobra.Command{
	Use:   "watch <workflow-id>",
	Short: "Poll workflow until done, streaming status updates",
	Long:  "Polls a workflow's pipeline status at regular intervals until it completes. Returns final status.",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent watch abc123-def456
  sem-agent watch abc123-def456 --interval 10s`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		interval, err := time.ParseDuration(watchIntervalFlag)
		if err != nil {
			interval = 30 * time.Second
		}
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}

		wfID := args[0]
		c := client.New()

		// Get workflow to find initial pipeline
		wfResp, err := c.Get("plumber-workflows", wfID)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if wfResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", wfResp.StatusCode, string(wfResp.Body)), wfResp.StatusCode)
			return fmt.Errorf("API returned %d", wfResp.StatusCode)
		}

		var wf struct {
			Workflow struct {
				InitialPplID string `json:"initial_ppl_id"`
				ProjectID    string `json:"project_id"`
			} `json:"workflow"`
		}
		if err := json.Unmarshal(wfResp.Body, &wf); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		type pollResult struct {
			WorkflowID string `json:"workflow_id"`
			PipelineID string `json:"pipeline_id"`
			Name       string `json:"name"`
			State      string `json:"state"`
			Result     string `json:"result,omitempty"`
			Elapsed    string `json:"elapsed"`
			Iteration  int    `json:"iteration"`
		}

		start := time.Now()
		for i := 1; ; i++ {
			// Get all pipelines for this workflow to track promotions too
			params := url.Values{}
			params.Set("project_id", wf.Workflow.ProjectID)
			params.Set("wf_id", wfID)
			pplListResp, err := c.ListWithParams("pipelines", params)

			var activePipelines []struct {
				PplID string `json:"ppl_id"`
				Name  string `json:"name"`
				State string `json:"state"`
				Result string `json:"result"`
			}

			if err == nil && pplListResp.StatusCode == 200 {
				json.Unmarshal(pplListResp.Body, &activePipelines)
			}

			// Check if all pipelines are done
			allDone := true
			var latestRunning string
			for _, p := range activePipelines {
				if p.State != "DONE" && p.State != "done" {
					allDone = false
					latestRunning = p.Name
				}
			}

			elapsed := time.Since(start).Round(time.Second)

			if allDone && len(activePipelines) > 0 {
				// Summarize final state
				type pplSummary struct {
					ID     string `json:"id"`
					Name   string `json:"name"`
					State  string `json:"state"`
					Result string `json:"result"`
				}
				summaries := make([]pplSummary, 0, len(activePipelines))
				for _, p := range activePipelines {
					summaries = append(summaries, pplSummary{
						ID: p.PplID, Name: p.Name, State: p.State, Result: p.Result,
					})
				}
				output.Result(map[string]any{
					"workflow_id": wfID,
					"status":     "done",
					"elapsed":    elapsed.String(),
					"iterations": i,
					"pipelines":  summaries,
				})
				return nil
			}

			// Emit progress
			pr := pollResult{
				WorkflowID: wfID,
				PipelineID: wf.Workflow.InitialPplID,
				Name:       latestRunning,
				State:      "running",
				Elapsed:    elapsed.String(),
				Iteration:  i,
			}
			if formatFlag == "table" {
				fmt.Printf("[%s] iteration %d — %s running...\n", elapsed, i, latestRunning)
			} else {
				b, _ := json.Marshal(pr)
				fmt.Println(string(b))
			}

			time.Sleep(interval)
		}
	},
}

func init() {
	watchCmd.Flags().StringVar(&watchIntervalFlag, "interval", "30s", "polling interval (e.g. 10s, 1m)")
	rootCmd.AddCommand(watchCmd)
}
