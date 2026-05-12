package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var apiSpecCmd = &cobra.Command{
	Use:   "api-spec",
	Short: "Fetch the Semaphore v2 OpenAPI specification",
	Example: `  sem-ai api-spec
  sem-ai api-spec --format yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		if err := c.ResolveOrgID(); err != nil {
			output.Error("org_error", err.Error(), 1)
			return err
		}
		resp, err := c.ListVersioned("v2", "api-spec")
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
			// Non-JSON (YAML OpenAPI) — wrap as string result
			output.Result(map[string]string{"format": "yaml", "spec": string(resp.Body)})
			return nil
		}
		output.Result(result)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(apiSpecCmd)
}
