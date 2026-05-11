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

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deployment target operations — visibility and management",
}

var deployTargetsProjectFlag string

var deployTargetsCmd = &cobra.Command{
	Use:   "targets",
	Short: "List deployment targets for a project",
	Example: `  sem-agent deploy targets --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		projectID, err := resolveProjectID(deployTargetsProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		params := url.Values{}
		params.Set("project_id", projectID)
		resp, err := c.ListWithParams("deployment_targets", params)
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

var deployShowCmd = &cobra.Command{
	Use:     "show <target-id>",
	Short:   "Show deployment target details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent deploy show <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("deployment_targets", args[0])
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

var deployHistoryCmd = &cobra.Command{
	Use:     "history <target-id>",
	Short:   "Show deployment history for a target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent deploy history <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("deployment_targets", args[0]+"/history")
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

var deployActivateCmd = &cobra.Command{
	Use:     "activate <target-id>",
	Short:   "Activate a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent deploy activate <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Patch("deployment_targets", args[0]+"/activate", nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "activated", "target_id": args[0]})
		return nil
	},
}

var deployDeactivateCmd = &cobra.Command{
	Use:     "deactivate <target-id>",
	Short:   "Deactivate a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent deploy deactivate <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Patch("deployment_targets", args[0]+"/deactivate", nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deactivated", "target_id": args[0]})
		return nil
	},
}

var deployDeleteCmd = &cobra.Command{
	Use:     "delete <target-id>",
	Short:   "Delete a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent deploy delete <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		token := client.NewRequestToken()
		resp, err := c.DeleteWithParams("deployment_targets", args[0], url.Values{"unique_token": {token}})
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "target_id": args[0]})
		return nil
	},
}

var (
	deployCreateProjectFlag string
	deployCreateDescFlag    string
	deployCreateURLFlag     string
	deployCreateBranchFlag  []string
)

var deployCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a deployment target",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent deploy create staging --project my-app --url https://staging.example.com
  sem-agent deploy create production --project my-app --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		projectID, err := resolveProjectID(deployCreateProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		body := map[string]any{
			"project_id":  projectID,
			"name":        args[0],
			"description": deployCreateDescFlag,
			"url":         deployCreateURLFlag,
		}
		if len(deployCreateBranchFlag) > 0 {
			body["bookmark_parameter1"] = deployCreateBranchFlag[0]
		}

		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("deployment_targets", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "created", "name": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

func init() {
	deployTargetsCmd.Flags().StringVar(&deployTargetsProjectFlag, "project", "", "project name or ID (required)")
	deployCreateCmd.Flags().StringVar(&deployCreateProjectFlag, "project", "", "project name or ID (required)")
	deployCreateCmd.Flags().StringVar(&deployCreateDescFlag, "description", "", "target description")
	deployCreateCmd.Flags().StringVar(&deployCreateURLFlag, "url", "", "target URL")
	deployCreateCmd.Flags().StringArrayVar(&deployCreateBranchFlag, "branch", nil, "allowed branches")

	deployCmd.AddCommand(deployTargetsCmd)
	deployCmd.AddCommand(deployShowCmd)
	deployCmd.AddCommand(deployHistoryCmd)
	deployCmd.AddCommand(deployActivateCmd)
	deployCmd.AddCommand(deployDeactivateCmd)
	deployCmd.AddCommand(deployCreateCmd)
	deployCmd.AddCommand(deployDeleteCmd)
	rootCmd.AddCommand(deployCmd)
}
