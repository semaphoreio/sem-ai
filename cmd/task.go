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

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Scheduled task (periodic job) operations",
}

var taskProjectFlag string

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled tasks for a project",
	Example: `  sem-agent task list --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		projectID, err := resolveProjectID(taskProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		params := url.Values{}
		params.Set("project_id", projectID)
		resp, err := c.ListWithParams("tasks", params)
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

var taskShowCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show scheduled task details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent task show <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("tasks", args[0])
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

var taskRunCmd = &cobra.Command{
	Use:     "run <id>",
	Short:   "Trigger a scheduled task to run now",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent task run <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.PostAction("tasks", args[0], "run_now", nil)
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
			output.Result(map[string]string{"status": "triggered", "task_id": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

var taskDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a scheduled task",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent task delete <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Delete("tasks", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "task_id": args[0]})
		return nil
	},
}

var (
	taskCreateProjectFlag  string
	taskCreateBranchFlag   string
	taskCreateFileFlag     string
	taskCreateCronFlag     string
	taskCreateDescFlag     string
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled task (periodic job)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent task create nightly-tests --project my-app --branch main --file .semaphore/nightly.yml --cron "0 2 * * *"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		projectID, err := resolveProjectID(taskCreateProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		body := map[string]any{
			"project_id":    projectID,
			"name":          args[0],
			"branch":        taskCreateBranchFlag,
			"pipeline_file": taskCreateFileFlag,
			"recurring":     taskCreateCronFlag != "",
			"description":   taskCreateDescFlag,
		}
		if taskCreateCronFlag != "" {
			body["expression"] = taskCreateCronFlag
		}

		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		if err := c.ResolveOrgID(); err != nil {
			output.Error("org_error", err.Error(), 1)
			return err
		}
		resp, err := c.PostVersioned("v2", "tasks", bodyBytes)
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
	taskListCmd.Flags().StringVar(&taskProjectFlag, "project", "", "project name or ID (required)")
	taskCreateCmd.Flags().StringVar(&taskCreateProjectFlag, "project", "", "project name or ID (required)")
	taskCreateCmd.Flags().StringVar(&taskCreateBranchFlag, "branch", "main", "branch to run on")
	taskCreateCmd.Flags().StringVar(&taskCreateFileFlag, "file", ".semaphore/semaphore.yml", "pipeline YAML file")
	taskCreateCmd.Flags().StringVar(&taskCreateCronFlag, "cron", "", "cron expression for recurring tasks")
	taskCreateCmd.Flags().StringVar(&taskCreateDescFlag, "description", "", "task description")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskRunCmd)
	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	rootCmd.AddCommand(taskCmd)
}
