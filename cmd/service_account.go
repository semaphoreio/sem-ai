package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var serviceAccountCmd = &cobra.Command{
	Use:   "service-account",
	Short: "Service account management — create limited-permission tokens",
}

var serviceAccountListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List service accounts in the organization",
	Example: `  sem-ai service-account list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.List("service_accounts")
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var serviceAccountCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a service account",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai service-account create ci-bot
  sem-ai service-account create ci-bot --description "Bot for CI pipelines"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]string{
			"name":        args[0],
			"description": saCreateDescFlag,
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("service_accounts", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var serviceAccountShowCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show service account details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai service-account show <service-account-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Get("service_accounts", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var serviceAccountUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a service account",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai service-account update <id> --name "new-name"
  sem-ai service-account update <id> --description "new description"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		if saUpdateNameFlag == "" && saUpdateDescFlag == "" {
			return fmt.Errorf("nothing to update — provide --name and/or --description")
		}
		c := client.New()
		body := map[string]string{}
		if saUpdateNameFlag != "" {
			body["name"] = saUpdateNameFlag
		}
		if saUpdateDescFlag != "" {
			body["description"] = saUpdateDescFlag
		}
		// The API treats update as a full replace and rejects a blank name. When
		// --name is omitted (e.g. a description-only update), preserve the current
		// name so it isn't cleared.
		if _, ok := body["name"]; !ok {
			cur, err := c.Get("service_accounts", args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if cur.StatusCode != 200 {
				output.Error("api_error", fmt.Sprintf("HTTP %d: %s", cur.StatusCode, string(cur.Body)), cur.StatusCode)
				return fmt.Errorf("API returned %d", cur.StatusCode)
			}
			var existing struct {
				Name string `json:"name"`
			}
			json.Unmarshal(cur.Body, &existing)
			body["name"] = existing.Name
		}
		bodyBytes, _ := json.Marshal(body)
		resp, err := c.Patch("service_accounts", args[0], bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var serviceAccountDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a service account",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai service-account delete <service-account-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Delete("service_accounts", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "id": args[0]})
		return nil
	},
}

var serviceAccountDeactivateCmd = &cobra.Command{
	Use:     "deactivate <id>",
	Short:   "Deactivate a service account",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai service-account deactivate <service-account-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		u := fmt.Sprintf("service_accounts/%s/deactivate", args[0])
		resp, err := c.Post(u, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deactivated", "id": args[0]})
		return nil
	},
}

var serviceAccountReactivateCmd = &cobra.Command{
	Use:     "reactivate <id>",
	Short:   "Reactivate a service account",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai service-account reactivate <service-account-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		u := fmt.Sprintf("service_accounts/%s/reactivate", args[0])
		resp, err := c.Post(u, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "reactivated", "id": args[0]})
		return nil
	},
}

var serviceAccountRegenerateTokenCmd = &cobra.Command{
	Use:     "regenerate-token <id>",
	Short:   "Regenerate API token for a service account",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai service-account regenerate-token <service-account-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		u := fmt.Sprintf("service_accounts/%s/regenerate_token", args[0])
		resp, err := c.Post(u, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var (
	saCreateDescFlag string
	saUpdateNameFlag string
	saUpdateDescFlag string
)

func init() {
	serviceAccountCreateCmd.Flags().StringVar(&saCreateDescFlag, "description", "", "service account description")
	serviceAccountUpdateCmd.Flags().StringVar(&saUpdateNameFlag, "name", "", "new service account name")
	serviceAccountUpdateCmd.Flags().StringVar(&saUpdateDescFlag, "description", "", "new service account description")

	serviceAccountCmd.AddCommand(serviceAccountListCmd)
	serviceAccountCmd.AddCommand(serviceAccountCreateCmd)
	serviceAccountCmd.AddCommand(serviceAccountShowCmd)
	serviceAccountCmd.AddCommand(serviceAccountUpdateCmd)
	serviceAccountCmd.AddCommand(serviceAccountDeleteCmd)
	serviceAccountCmd.AddCommand(serviceAccountDeactivateCmd)
	serviceAccountCmd.AddCommand(serviceAccountReactivateCmd)
	serviceAccountCmd.AddCommand(serviceAccountRegenerateTokenCmd)
	rootCmd.AddCommand(serviceAccountCmd)
}
