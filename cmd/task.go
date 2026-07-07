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

var (
	taskRunParamsFlag []string
	taskRunBranchFlag string
	taskRunFileFlag   string
)

var taskRunCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Trigger a scheduled task to run now",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task run <task-id>
  sem-ai task run <task-id> --param KEY=VALUE --param KEY2=VALUE2
  sem-ai task run <task-id> --branch main --pipeline-file .semaphore/pipeline.yml --param KEY=VALUE`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}

		// Build a run_now body only when overrides are supplied; otherwise
		// send nil to preserve the parameter-less default behaviour.
		var body []byte
		if len(taskRunParamsFlag) > 0 || taskRunBranchFlag != "" || taskRunFileFlag != "" {
			reqBody := map[string]any{}
			if taskRunBranchFlag != "" {
				reqBody["branch"] = taskRunBranchFlag
			}
			if taskRunFileFlag != "" {
				reqBody["pipeline_file"] = taskRunFileFlag
			}
			params := map[string]string{}
			for _, p := range taskRunParamsFlag {
				i := strings.IndexByte(p, '=')
				if i <= 0 {
					return fmt.Errorf("invalid --param %q: expected KEY=VALUE", p)
				}
				params[p[:i]] = p[i+1:]
			}
			if len(params) > 0 {
				reqBody["parameters"] = params
			}
			body, _ = json.Marshal(reqBody)
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

// taskProjectID resolves the owning project of a task via the v1alpha
// describe endpoint, so v2 calls (which nest tasks under a project path)
// don't need an explicit --project flag.
func taskProjectID(c *client.Client, taskID string) (string, error) {
	resp, err := c.Get("tasks", taskID)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	var described struct {
		Schedule struct {
			ProjectID string `json:"project_id"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(resp.Body, &described); err != nil {
		return "", fmt.Errorf("cannot parse task description: %w", err)
	}
	if described.Schedule.ProjectID == "" {
		return "", fmt.Errorf("task %s has no project_id in describe response", taskID)
	}
	return described.Schedule.ProjectID, nil
}

// patchTaskSpec sends a partial spec to the v2 task update endpoint.
// Fields absent from spec keep their current value (server-side merge).
func patchTaskSpec(taskID string, spec map[string]any) error {
	c := client.New()
	projectID, err := taskProjectID(c, taskID)
	if err != nil {
		output.Error("api_error", err.Error(), 1)
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"apiVersion": "v2",
		"kind":       "Task",
		"spec":       spec,
	})
	resp, err := c.PatchVersioned("v2", fmt.Sprintf("projects/%s/tasks", projectID), taskID, body)
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

var taskDeactivateCmd = &cobra.Command{
	Use:     "deactivate <id>",
	Short:   "Deactivate (pause) a scheduled task",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai task deactivate <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		return patchTaskSpec(args[0], map[string]any{"paused": true})
	},
}

var taskActivateCmd = &cobra.Command{
	Use:     "activate <id>",
	Short:   "Activate (unpause) a scheduled task",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai task activate <task-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		return patchTaskSpec(args[0], map[string]any{"paused": false})
	},
}

var (
	taskUpdateNameFlag     string
	taskUpdateDescFlag     string
	taskUpdateBranchFlag   string
	taskUpdateTagFlag      string
	taskUpdateFileFlag     string
	taskUpdateCronFlag     string
	taskUpdateParamDefFlag []string
)

var taskUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a scheduled task (PATCH — only provided flags change)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task update <task-id> --cron "0 4 * * *"
  sem-ai task update <task-id> --branch develop --file .semaphore/nightly.yml
  sem-ai task update <task-id> --param-def ENVIRONMENT=staging --param-def VERSION`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		if taskUpdateBranchFlag != "" && taskUpdateTagFlag != "" {
			return fmt.Errorf("--branch and --tag are mutually exclusive")
		}
		spec := map[string]any{}
		if taskUpdateNameFlag != "" {
			spec["name"] = taskUpdateNameFlag
		}
		if taskUpdateDescFlag != "" {
			spec["description"] = taskUpdateDescFlag
		}
		if taskUpdateBranchFlag != "" {
			spec["reference"] = map[string]string{"type": "branch", "name": taskUpdateBranchFlag}
		}
		if taskUpdateTagFlag != "" {
			spec["reference"] = map[string]string{"type": "tag", "name": taskUpdateTagFlag}
		}
		if taskUpdateFileFlag != "" {
			spec["pipeline_file"] = taskUpdateFileFlag
		}
		if taskUpdateCronFlag != "" {
			spec["cron_schedule"] = taskUpdateCronFlag
		}
		if len(taskUpdateParamDefFlag) > 0 {
			defs, err := parseParamDefs(taskUpdateParamDefFlag)
			if err != nil {
				return err
			}
			params := make([]map[string]any, 0, len(defs))
			for _, d := range defs {
				p := map[string]any{"name": d.Name, "required": d.Required}
				if !d.Required {
					p["default_value"] = d.DefaultValue
				}
				params = append(params, p)
			}
			spec["parameters"] = params
		}
		if len(spec) == 0 {
			return fmt.Errorf("nothing to update — pass at least one of --name, --description, --branch, --tag, --file, --cron, --param-def")
		}
		return patchTaskSpec(args[0], spec)
	},
}

var (
	taskCreateProjectFlag  string
	taskCreateBranchFlag   string
	taskCreateFileFlag     string
	taskCreateCronFlag     string
	taskCreateParamDefFlag []string
)

var taskCreateCmd = &cobra.Command{
	Use:     "create <name>",
	Short:   "Create a scheduled task (periodic job)",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai task create nightly-tests --project my-app --branch main --file .semaphore/nightly.yml --cron "0 2 * * *"
  sem-ai task create deploy-env --branch main --file .semaphore/deploy.yml --param-def ENVIRONMENT=staging --param-def VERSION`,
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

		paramDefs, err := parseParamDefs(taskCreateParamDefFlag)
		if err != nil {
			return err
		}

		yml := buildScheduleYAML(args[0], projectName, taskCreateBranchFlag, taskCreateFileFlag, taskCreateCronFlag, paramDefs)
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

// taskParamDef is a parameter definition on a task (not a run-time value).
type taskParamDef struct {
	Name         string
	Required     bool
	DefaultValue string
}

// parseParamDefs turns repeatable --param-def flags into definitions.
// Bare NAME declares a required parameter; NAME=DEFAULT declares an
// optional one with a default value.
func parseParamDefs(defs []string) ([]taskParamDef, error) {
	out := make([]taskParamDef, 0, len(defs))
	for _, d := range defs {
		i := strings.IndexByte(d, '=')
		switch {
		case i == 0 || d == "":
			return nil, fmt.Errorf("invalid --param-def %q: expected NAME or NAME=DEFAULT", d)
		case i < 0:
			out = append(out, taskParamDef{Name: d, Required: true})
		default:
			out = append(out, taskParamDef{Name: d[:i], Required: false, DefaultValue: d[i+1:]})
		}
	}
	return out, nil
}

// buildScheduleYAML renders the apiVersion/kind/metadata/spec doc that
// v1alpha POST /tasks (apply schedule) expects as yml_definition.
// apiVersion v1.1 enables one-off tasks via recurring:false (no `at`)
// and parameter definitions.
func buildScheduleYAML(name, project, branch, pipelineFile, cron string, params []taskParamDef) string {
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
	if len(params) > 0 {
		b.WriteString("  parameters:\n")
		for _, p := range params {
			fmt.Fprintf(&b, "    - name: %s\n", yamlEscape(p.Name))
			fmt.Fprintf(&b, "      required: %t\n", p.Required)
			if !p.Required {
				fmt.Fprintf(&b, "      default_value: %s\n", yamlEscape(p.DefaultValue))
			}
		}
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
	taskCreateCmd.Flags().StringArrayVar(&taskCreateParamDefFlag, "param-def", nil, "parameter definition as NAME (required) or NAME=DEFAULT (optional with default); repeatable")

	taskRunCmd.Flags().StringArrayVar(&taskRunParamsFlag, "param", nil, "task parameter as KEY=VALUE (repeatable)")
	taskRunCmd.Flags().StringVar(&taskRunBranchFlag, "branch", "", "git ref the task pipeline runs on (e.g. master); defaults to the task's configured branch")
	taskRunCmd.Flags().StringVar(&taskRunFileFlag, "pipeline-file", "", "pipeline YAML file the task runs; defaults to the task's configured file")

	taskUpdateCmd.Flags().StringVar(&taskUpdateNameFlag, "name", "", "new task name")
	taskUpdateCmd.Flags().StringVar(&taskUpdateDescFlag, "description", "", "new task description")
	taskUpdateCmd.Flags().StringVar(&taskUpdateBranchFlag, "branch", "", "branch to run on (mutually exclusive with --tag)")
	taskUpdateCmd.Flags().StringVar(&taskUpdateTagFlag, "tag", "", "tag to run on (mutually exclusive with --branch)")
	taskUpdateCmd.Flags().StringVar(&taskUpdateFileFlag, "file", "", "pipeline YAML file")
	taskUpdateCmd.Flags().StringVar(&taskUpdateCronFlag, "cron", "", "cron expression")
	taskUpdateCmd.Flags().StringArrayVar(&taskUpdateParamDefFlag, "param-def", nil, "parameter definition as NAME (required) or NAME=DEFAULT (optional with default); replaces ALL existing parameter definitions; repeatable")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskRunCmd)
	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskUpdateCmd)
	taskCmd.AddCommand(taskActivateCmd)
	taskCmd.AddCommand(taskDeactivateCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	rootCmd.AddCommand(taskCmd)
}
