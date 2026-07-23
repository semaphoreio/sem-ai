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

var healthProjectFlag string

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Project health summary: recent pass rate, failures, deployment status",
	Long: `Compound command: aggregates recent workflow results, test failures,
and deployment target status into a single health report.`,
	Example: `  sem-ai health --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}

		project := healthProjectFlag
		if project == "" {
			p, err := detectProject()
			if err != nil {
				output.Error("context_error", "could not detect project; use --project", 1)
				return err
			}
			project = p
		}

		projectID, err := resolveProjectID(project)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()

		// Fetch recent workflows
		params := url.Values{}
		params.Set("project_id", projectID)
		wfResp, err := c.ListWithParams("plumber-workflows", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var workflows []struct {
			WfID         string `json:"wf_id"`
			InitialPplID string `json:"initial_ppl_id"`
			BranchName   string `json:"branch_name"`
		}
		if wfResp.StatusCode == 200 {
			json.Unmarshal(wfResp.Body, &workflows)
		}

		// Analyze recent pipelines (up to 10)
		limit := 10
		if limit > len(workflows) {
			limit = len(workflows)
		}

		passed, failed, other := 0, 0, 0
		for _, wf := range workflows[:limit] {
			pplParams := url.Values{}
			pplParams.Set("detailed", "true")
			pplResp, err := c.ListWithParams("pipelines/"+wf.InitialPplID, pplParams)
			if err != nil || pplResp.StatusCode != 200 {
				other++
				continue
			}
			var ppl struct {
				Pipeline struct {
					Result string `json:"result"`
				} `json:"pipeline"`
			}
			json.Unmarshal(pplResp.Body, &ppl)
			switch ppl.Pipeline.Result {
			case "passed":
				passed++
			case "failed":
				failed++
			default:
				other++
			}
		}

		passRate := 0.0
		if passed+failed > 0 {
			passRate = float64(passed) / float64(passed+failed) * 100
		}

		// Check deploy targets
		dtParams := url.Values{}
		dtParams.Set("project_id", projectID)
		dtResp, _ := c.ListWithParams("deployment_targets", dtParams)
		deployTargets := 0
		if dtResp != nil && dtResp.StatusCode == 200 {
			var targets []any
			json.Unmarshal(dtResp.Body, &targets)
			deployTargets = len(targets)
		}

		verdict := "healthy"
		if passRate < 50 {
			verdict = "unhealthy"
		} else if passRate < 80 {
			verdict = "degraded"
		}

		output.Result(map[string]any{
			"project":          project,
			"verdict":          verdict,
			"recent_workflows": limit,
			"passed":           passed,
			"failed":           failed,
			"other":            other,
			"pass_rate":        fmt.Sprintf("%.0f%%", passRate),
			"deploy_targets":   deployTargets,
			"total_workflows":  len(workflows),
		})
		return nil
	},
}

func init() {
	healthCmd.Flags().StringVar(&healthProjectFlag, "project", "", "project name (auto-detected from git if omitted)")
	rootCmd.AddCommand(healthCmd)
}
