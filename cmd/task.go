package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

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

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled tasks for a project",
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
	Use:     "run <id>",
	Short:   "Trigger a scheduled task to run now",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai task run <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
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
	taskCreateProjectFlag string
	taskCreateBranchFlag  string
	taskCreateFileFlag    string
	taskCreateCronFlag    string
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled task (periodic job)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task create nightly-tests --project my-app --branch main --file .semaphore/nightly.yml --cron "0 2 * * *"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectName, projectID, err := resolveProject(taskCreateProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()

		// Pre-check: v1alpha apply is upsert; match v2 create semantics by failing on duplicate.
		params := url.Values{}
		params.Set("project_id", projectID)
		listResp, err := c.ListWithParams("tasks", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if listResp.StatusCode == 200 {
			var entries []struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			}
			if err := json.Unmarshal(listResp.Body, &entries); err == nil {
				for _, e := range entries {
					if e.Name == args[0] {
						err := fmt.Errorf("task %q already exists (id=%s); use a different name or delete it first", args[0], e.ID)
						output.Error("conflict", err.Error(), 1)
						return err
					}
				}
			}
		}

		yml := buildScheduleYAML(args[0], projectName, taskCreateBranchFlag, taskCreateFileFlag, taskCreateCronFlag)
		bodyBytes, _ := json.Marshal(map[string]string{"yml_definition": yml})

		resp, err := c.Post("tasks", bodyBytes)
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

// buildScheduleYAML renders the apiVersion/kind/metadata/spec doc that
// v1alpha POST /tasks (apply schedule) expects as yml_definition.
// apiVersion v1.1 enables one-off tasks via recurring:false (no `at`).
func buildScheduleYAML(name, project, branch, pipelineFile, cron string) string {
	recurring := cron != ""
	var b strings.Builder
	b.WriteString("apiVersion: v1.1\n")
	b.WriteString("kind: Periodic\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", yamlEscape(name))
	b.WriteString("spec:\n")
	fmt.Fprintf(&b, "  project: %s\n", yamlEscape(project))
	fmt.Fprintf(&b, "  branch: %s\n", yamlEscape(branch))
	fmt.Fprintf(&b, "  pipeline_file: %s\n", yamlEscape(pipelineFile))
	fmt.Fprintf(&b, "  recurring: %t\n", recurring)
	if recurring {
		fmt.Fprintf(&b, "  at: %q\n", cron)
	}
	return b.String()
}

// yamlEscape quotes a scalar if it contains characters that would otherwise
// break plain YAML parsing. Conservative: quote anything non-trivial.
func yamlEscape(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`\n\t") || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "? ") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func init() {
	taskListCmd.Flags().StringVar(&taskProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	taskCreateCmd.Flags().StringVar(&taskCreateProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	taskCreateCmd.Flags().StringVar(&taskCreateBranchFlag, "branch", "main", "branch to run on")
	taskCreateCmd.Flags().StringVar(&taskCreateFileFlag, "file", ".semaphore/semaphore.yml", "pipeline YAML file")
	taskCreateCmd.Flags().StringVar(&taskCreateCronFlag, "cron", "", "cron expression for recurring tasks")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskRunCmd)
	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	rootCmd.AddCommand(taskCmd)
}
