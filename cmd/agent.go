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

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Self-hosted agent management",
}

var agentTypesCmd = &cobra.Command{
	Use:     "types",
	Short:   "List self-hosted agent types",
	Example: `  sem-agent agent types`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.List("self_hosted_agent_types")
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
	},
}

var agentShowCmd = &cobra.Command{
	Use:     "show <type-name>",
	Short:   "Show agent type details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent agent show s1-my-type`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("self_hosted_agent_types", args[0])
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
	},
}

var agentListTypeFlag string

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agents for a given type",
	Example: `  sem-agent agent list --type s1-my-type`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		if agentListTypeFlag == "" {
			output.Error("invalid_args", "--type is required", 1)
			return fmt.Errorf("--type is required")
		}
		c := client.New()
		params := url.Values{}
		params.Set("agent_type_name", agentListTypeFlag)
		resp, err := c.ListWithParams("agents", params)
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
	},
}

var agentTypeDeleteCmd = &cobra.Command{
	Use:     "delete <type-name>",
	Short:   "Delete a self-hosted agent type",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent agent delete s1-my-type`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Delete("self_hosted_agent_types", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "name": args[0]})
		return nil
	},
}

func init() {
	agentListCmd.Flags().StringVar(&agentListTypeFlag, "type", "", "agent type name (required)")

	agentCmd.AddCommand(agentTypesCmd)
	agentCmd.AddCommand(agentShowCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentTypeDeleteCmd)
	rootCmd.AddCommand(agentCmd)
}
