package cmd

import (
	"fmt"
	"net/url"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var insightsCmd = &cobra.Command{
	Use:   "insights",
	Short: "Pipeline insights (Velocity): performance, reliability, frequency",
}

func insightsParams(pipelineFile, branch, from, to, aggregate string) url.Values {
	v := url.Values{}
	v.Set("pipeline_file", pipelineFile)
	if branch != "" {
		v.Set("branch", branch)
	}
	if from != "" {
		v.Set("from", from)
	}
	if to != "" {
		v.Set("to", to)
	}
	if aggregate != "" {
		v.Set("aggregate", aggregate)
	}
	return v
}

type insightsFlags struct {
	project, pipelineFile, branch, from, to, aggregate string
}

func runInsights(metric string, f *insightsFlags) error {
	if !config.IsConfigured() {
		return fmt.Errorf("not configured — run 'sem-ai connect' first")
	}
	if f.pipelineFile == "" {
		output.Error("usage_error", "--pipeline-file is required", 1)
		return fmt.Errorf("--pipeline-file is required")
	}
	projectID, err := resolveProjectID(f.project)
	if err != nil {
		output.Error("project_error", err.Error(), 1)
		return err
	}
	c := client.New()
	resp, err := c.ListWithParams(
		fmt.Sprintf("projects/%s/insights/%s", projectID, metric),
		insightsParams(f.pipelineFile, f.branch, f.from, f.to, f.aggregate),
	)
	if err != nil {
		output.Error("api_error", err.Error(), 1)
		return err
	}
	return emitJSON(resp)
}

func newInsightsSubCmd(metric, short string) (*cobra.Command, *insightsFlags) {
	f := &insightsFlags{}
	cmd := &cobra.Command{
		Use:     metric,
		Short:   short,
		Example: fmt.Sprintf("  sem-ai insights %s --project my-project --pipeline-file .semaphore/semaphore.yml --branch main", metric),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInsights(metric, f)
		},
	}
	cmd.Flags().StringVar(&f.project, "project", "", "project name or ID (required)")
	cmd.Flags().StringVar(&f.pipelineFile, "pipeline-file", "", "pipeline YAML path (required, e.g. .semaphore/semaphore.yml)")
	cmd.Flags().StringVar(&f.branch, "branch", "", "branch name")
	cmd.Flags().StringVar(&f.from, "from", "", "start date YYYY-MM-DD")
	cmd.Flags().StringVar(&f.to, "to", "", "end date YYYY-MM-DD")
	cmd.Flags().StringVar(&f.aggregate, "aggregate", "daily", "aggregation: daily|range")
	return cmd, f
}

func init() {
	perf, _ := newInsightsSubCmd("performance", "Pipeline duration metrics (mean/median/p95 seconds)")
	reliab, _ := newInsightsSubCmd("reliability", "Pipeline pass/fail counts")
	freq, _ := newInsightsSubCmd("frequency", "Pipeline run frequency")

	insightsCmd.AddCommand(perf)
	insightsCmd.AddCommand(reliab)
	insightsCmd.AddCommand(freq)
	rootCmd.AddCommand(insightsCmd)
}
