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

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Scheduled task (periodic job) operations",
}

var taskProjectFlag string
var taskRunParamsFlag map[string]string

var taskListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List scheduled tasks for a project",
	Example: `  sem-ai task list --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
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
	Example: `  sem-ai task show <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
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
	Use:   "run <id>",
	Short: "Trigger a scheduled task to run now",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task run <task-id>
  sem-ai task run <task-id> --param ENV=staging --param SERVICE=api --param BRANCH=feature-x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}

		// Tasks with declared required parameters need a body — POSTing nil
		// causes the server to reject the run with INVALID_ARGUMENT. pflag's
		// StringToStringVar handles --param KEY=VAL parsing + validation.
		var body []byte
		if len(taskRunParamsFlag) > 0 {
			b, err := json.Marshal(map[string]any{"parameters": taskRunParamsFlag})
			if err != nil {
				return fmt.Errorf("marshal parameters: %w", err)
			}
			body = b
		}

		c := client.New()
		resp, err := c.PostAction("tasks", args[0], "run_now", body)
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
	Example: `  sem-ai task delete <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
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
	taskCreateProjectFlag        string
	taskCreateBranchFlag         string
	taskCreateFileFlag           string
	taskCreateCronFlag           string
	taskCreateDescFlag           string
	taskCreateRequiredParamsFlag []string
	taskCreateOptionalParamsFlag []string
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled task (periodic job)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task create nightly-tests --project my-app --branch main --file .semaphore/nightly.yml --cron "0 2 * * *"
  sem-ai task create deploy --project my-app --branch main --file .semaphore/deploy.yml --required-param ENV --required-param SERVICE`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(taskCreateProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		// v2 task create body — K8s-style envelope with a `spec` block.
		// Project goes in the URL path (route: /api/v2/projects/:project_id_or_name/tasks).
		spec := map[string]any{
			"name":          args[0],
			"description":   taskCreateDescFlag,
			"pipeline_file": taskCreateFileFlag,
			"reference": map[string]any{
				"type": "branch",
				"name": taskCreateBranchFlag,
			},
		}
		if taskCreateCronFlag != "" {
			spec["cron_schedule"] = taskCreateCronFlag
		}

		// Declare task parameters (v2 schema: array of {name, required, ...}).
		var params []map[string]any
		for _, n := range taskCreateRequiredParamsFlag {
			params = append(params, map[string]any{"name": n, "required": true})
		}
		for _, n := range taskCreateOptionalParamsFlag {
			params = append(params, map[string]any{"name": n, "required": false})
		}
		if len(params) > 0 {
			spec["parameters"] = params
		}

		body := map[string]any{
			"apiVersion": "v2",
			"kind":       "Task",
			"spec":       spec,
		}

		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		if err := c.ResolveOrgID(); err != nil {
			output.Error("org_error", err.Error(), 1)
			return err
		}
		resp, err := c.PostVersioned("v2", fmt.Sprintf("projects/%s/tasks", projectID), bodyBytes)
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
	taskRunCmd.Flags().StringToStringVar(&taskRunParamsFlag, "param", nil, "task parameter in KEY=VAL form (repeatable); required for tasks that declare parameters")
	taskCreateCmd.Flags().StringVar(&taskCreateProjectFlag, "project", "", "project name or ID (required)")
	taskCreateCmd.Flags().StringVar(&taskCreateBranchFlag, "branch", "main", "branch to run on")
	taskCreateCmd.Flags().StringVar(&taskCreateFileFlag, "file", ".semaphore/semaphore.yml", "pipeline YAML file")
	taskCreateCmd.Flags().StringVar(&taskCreateCronFlag, "cron", "", "cron expression for recurring tasks")
	taskCreateCmd.Flags().StringVar(&taskCreateDescFlag, "description", "", "task description")
	taskCreateCmd.Flags().StringArrayVar(&taskCreateRequiredParamsFlag, "required-param", []string{}, "declare a required task parameter NAME (repeatable)")
	taskCreateCmd.Flags().StringArrayVar(&taskCreateOptionalParamsFlag, "optional-param", []string{}, "declare an optional task parameter NAME (repeatable)")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskRunCmd)
	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	rootCmd.AddCommand(taskCmd)
}
