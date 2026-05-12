package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var notificationCmd = &cobra.Command{
	Use:   "notification",
	Short: "Notification management — Slack, email, webhook",
}

var notificationListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List notification rules",
	Example: `  sem-ai notification list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.List("notifications")
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

var notificationShowCmd = &cobra.Command{
	Use:     "show <name>",
	Short:   "Show notification rule details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai notification show my-notification`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Get("notifications", args[0])
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

var notificationDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a notification rule",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai notification delete my-notification`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Delete("notifications", args[0])
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

var notificationCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a notification rule from a YAML/JSON file",
	Long:  "Creates a notification rule. Pass the notification spec as a JSON file via --file.",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai notification create my-notification --file notification.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		// For now, create a minimal notification — full YAML/JSON support in future
		output.Error("not_implemented", "notification create requires --file with full notification spec (not yet implemented — use sem CLI for now)", 1)
		return fmt.Errorf("not yet fully implemented")
	},
}

func init() {
	notificationCmd.AddCommand(notificationListCmd)
	notificationCmd.AddCommand(notificationShowCmd)
	notificationCmd.AddCommand(notificationCreateCmd)
	notificationCmd.AddCommand(notificationDeleteCmd)
	rootCmd.AddCommand(notificationCmd)
}
