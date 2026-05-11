package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var troubleshootCmd = &cobra.Command{
	Use:   "troubleshoot",
	Short: "Server-side diagnostics for workflows, pipelines, and jobs",
}

var troubleshootWorkflowCmd = &cobra.Command{
	Use:     "workflow <id>",
	Short:   "Troubleshoot a workflow",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent troubleshoot workflow <workflow-id>`,
	RunE:    troubleshootRun("workflow"),
}

var troubleshootPipelineCmd = &cobra.Command{
	Use:     "pipeline <id>",
	Short:   "Troubleshoot a pipeline",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent troubleshoot pipeline <pipeline-id>`,
	RunE:    troubleshootRun("pipeline"),
}

var troubleshootJobCmd = &cobra.Command{
	Use:     "job <id>",
	Short:   "Troubleshoot a job",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent troubleshoot job <job-id>`,
	RunE:    troubleshootRun("job"),
}

func troubleshootRun(kind string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("troubleshoot/"+kind, args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		json.Unmarshal(resp.Body, &result)
		output.Result(result)
		return nil
	}
}

func init() {
	troubleshootCmd.AddCommand(troubleshootWorkflowCmd)
	troubleshootCmd.AddCommand(troubleshootPipelineCmd)
	troubleshootCmd.AddCommand(troubleshootJobCmd)
	rootCmd.AddCommand(troubleshootCmd)
}
