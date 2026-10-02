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
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
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
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
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
		output.Result(withNotificationSkipFlags(result))
		return nil
	},
}

// taskNotificationSkipKeys are the per-task commit-status suppression flags
// v1alpha GET /tasks/:id reports under "schedule".
var taskNotificationSkipKeys = []string{"skip_scheduled_run_notifications", "skip_manual_run_notifications"}

// withNotificationSkipFlags makes both skip flags explicit on a task show
// response. Servers that predate the flags omit them and never skip commit
// statuses, so a missing key is reported as false.
func withNotificationSkipFlags(result any) any {
	top, ok := result.(map[string]any)
	if !ok {
		return result
	}
	schedule, ok := top["schedule"].(map[string]any)
	if !ok {
		return result
	}
	for _, k := range taskNotificationSkipKeys {
		if _, present := schedule[k]; !present {
			schedule[k] = false
		}
	}
	return result
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
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
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
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
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
	taskCreateProjectFlag       string
	taskCreateBranchFlag        string
	taskCreateFileFlag          string
	taskCreateCronFlag          string
	taskCreateParamDefFlag      []string
	taskCreateSkipScheduledFlag bool
	taskCreateSkipManualFlag    bool
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled task (periodic job)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai task create nightly-tests --project my-app --branch main --file .semaphore/nightly.yml --cron "0 2 * * *"
  sem-ai task create deploy-env --branch main --file .semaphore/deploy.yml --param-def ENVIRONMENT=staging --param-def VERSION
  sem-ai task create quiet-cron --branch main --file .semaphore/cron.yml --cron "*/10 * * * *" --skip-scheduled-run-notifications`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
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

		yml := buildScheduleYAML(args[0], projectName, taskCreateBranchFlag, taskCreateFileFlag, taskCreateCronFlag,
			taskCreateSkipScheduledFlag, taskCreateSkipManualFlag, paramDefs)
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
// apiVersion v1.1 enables one-off tasks via recurring:false (no `at`),
// parameter definitions, and the notification skip flags.
func buildScheduleYAML(name, project, branch, pipelineFile, cron string, skipScheduled, skipManual bool, params []taskParamDef) string {
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
	// Omitted keys mean "no change" to the v1.1 apply endpoint, so only emit
	// the skip flags when set; false is already the server-side default.
	if skipScheduled {
		b.WriteString("  skip_scheduled_run_notifications: true\n")
	}
	if skipManual {
		b.WriteString("  skip_manual_run_notifications: true\n")
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

var (
	taskUpdateSkipScheduledFlag string
	taskUpdateSkipManualFlag    string
)

var taskUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a scheduled task's notification skip flags",
	Long: `Update a scheduled task's notification skip flags in place, keeping its ID
and trigger history.

Each flag takes "true" or "false"; leave it out (or empty) to keep the current
value. The task is re-read first and re-applied with its id, project, branch,
schedule and pipeline file; parameters, description and paused state are left
out of the request, so the server keeps them as they are.`,
	Args: cobra.ExactArgs(1),
	Example: `  sem-ai task update <task-id> --skip-scheduled-run-notifications true
  sem-ai task update <task-id> --skip-scheduled-run-notifications false --skip-manual-run-notifications true`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		skipScheduled, err := parseOptionalBool("skip-scheduled-run-notifications", taskUpdateSkipScheduledFlag)
		if err != nil {
			output.Error("invalid_flag", err.Error(), 1)
			return err
		}
		skipManual, err := parseOptionalBool("skip-manual-run-notifications", taskUpdateSkipManualFlag)
		if err != nil {
			output.Error("invalid_flag", err.Error(), 1)
			return err
		}
		if skipScheduled == nil && skipManual == nil {
			err := fmt.Errorf("nothing to update; pass --skip-scheduled-run-notifications and/or --skip-manual-run-notifications as true or false")
			output.Error("invalid_flag", err.Error(), 1)
			return err
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
		var current struct {
			Schedule taskSchedule `json:"schedule"`
		}
		if err := json.Unmarshal(resp.Body, &current); err != nil || current.Schedule.ID == "" {
			err := fmt.Errorf("unexpected task show response for %s", args[0])
			output.Error("api_error", err.Error(), 1)
			return err
		}
		task := current.Schedule

		// The v1alpha task record carries only project_id; apply needs the name.
		projectName, _, err := resolveProject(task.ProjectID)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		yml := buildScheduleUpdateYAML(task, projectName, skipScheduled, skipManual)
		bodyBytes, _ := json.Marshal(map[string]string{"yml_definition": yml})
		postResp, err := c.Post("tasks", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if postResp.StatusCode != 200 && postResp.StatusCode != 201 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", postResp.StatusCode, string(postResp.Body)), postResp.StatusCode)
			return fmt.Errorf("API returned %d", postResp.StatusCode)
		}

		finalScheduled, finalManual := task.SkipScheduledRunNotifications, task.SkipManualRunNotifications
		if skipScheduled != nil {
			finalScheduled = *skipScheduled
		}
		if skipManual != nil {
			finalManual = *skipManual
		}
		output.Result(map[string]any{
			"status":                           "updated",
			"task_id":                          task.ID,
			"skip_scheduled_run_notifications": finalScheduled,
			"skip_manual_run_notifications":    finalManual,
		})
		return nil
	},
}

// taskSchedule is the subset of v1alpha GET /tasks/:id "schedule" that task
// update re-applies. Branch is the short name and is only set for
// refs/heads/* references; Reference is always the full ref.
type taskSchedule struct {
	ID                            string `json:"id"`
	Name                          string `json:"name"`
	ProjectID                     string `json:"project_id"`
	Branch                        string `json:"branch"`
	Reference                     string `json:"reference"`
	At                            string `json:"at"`
	PipelineFile                  string `json:"pipeline_file"`
	Recurring                     bool   `json:"recurring"`
	SkipScheduledRunNotifications bool   `json:"skip_scheduled_run_notifications"`
	SkipManualRunNotifications    bool   `json:"skip_manual_run_notifications"`
}

// parseOptionalBool reads a tri-state string flag: "" means unset (nil),
// otherwise "true" or "false". Update flags are strings rather than bools
// because the MCP adapter only forwards true booleans, so a bool could never
// clear a flag over MCP, and "" lets clients that fill optional parameters
// with defaults leave the stored value alone.
func parseOptionalBool(name, v string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return nil, nil
	case "true":
		b := true
		return &b, nil
	case "false":
		b := false
		return &b, nil
	default:
		return nil, fmt.Errorf("--%s must be true or false, got %q", name, v)
	}
}

// buildScheduleUpdateYAML renders a v1.1 apply doc that updates task in place:
// metadata.id selects the update path, and the update path only changes keys
// that are present. So it restates the fields apply validates (project,
// recurring, branch, at, pipeline_file) and emits only the skip flags being
// changed; parameters, description and paused are omitted and kept as stored.
func buildScheduleUpdateYAML(task taskSchedule, project string, skipScheduled, skipManual *bool) string {
	branch := task.Branch
	if branch == "" {
		// Non-branch refs (tags, PRs) have no short name; v1.1 keeps a value
		// starting with refs/ unchanged.
		branch = task.Reference
	}
	var b strings.Builder
	b.WriteString("apiVersion: v1.1\n")
	b.WriteString("kind: Periodic\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", yamlEscape(task.Name))
	fmt.Fprintf(&b, "  id: %s\n", yamlEscape(task.ID))
	b.WriteString("spec:\n")
	fmt.Fprintf(&b, "  project: %s\n", yamlEscape(project))
	fmt.Fprintf(&b, "  branch: %s\n", yamlEscape(branch))
	fmt.Fprintf(&b, "  pipeline_file: %s\n", yamlEscape(task.PipelineFile))
	fmt.Fprintf(&b, "  recurring: %t\n", task.Recurring)
	if task.Recurring {
		fmt.Fprintf(&b, "  at: %q\n", task.At)
	}
	if skipScheduled != nil {
		fmt.Fprintf(&b, "  skip_scheduled_run_notifications: %t\n", *skipScheduled)
	}
	if skipManual != nil {
		fmt.Fprintf(&b, "  skip_manual_run_notifications: %t\n", *skipManual)
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
	taskCreateCmd.Flags().BoolVar(&taskCreateSkipScheduledFlag, "skip-scheduled-run-notifications", false, "don't send commit statuses for pipelines this task starts on schedule (reruns included)")
	taskCreateCmd.Flags().BoolVar(&taskCreateSkipManualFlag, "skip-manual-run-notifications", false, "don't send commit statuses for pipelines this task starts manually: Run now, API, or CLI (reruns included)")

	taskRunCmd.Flags().StringArrayVar(&taskRunParamsFlag, "param", nil, "task parameter as KEY=VALUE (repeatable)")
	taskRunCmd.Flags().StringVar(&taskRunBranchFlag, "branch", "", "git ref the task pipeline runs on (e.g. master); defaults to the task's configured branch")
	taskRunCmd.Flags().StringVar(&taskRunFileFlag, "pipeline-file", "", "pipeline YAML file the task runs; defaults to the task's configured file")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskRunCmd)
	taskUpdateCmd.Flags().StringVar(&taskUpdateSkipScheduledFlag, "skip-scheduled-run-notifications", "", "true/false: don't send commit statuses for pipelines this task starts on schedule (reruns included); empty keeps the current value")
	taskUpdateCmd.Flags().StringVar(&taskUpdateSkipManualFlag, "skip-manual-run-notifications", "", "true/false: don't send commit statuses for pipelines this task starts manually: Run now, API, or CLI (reruns included); empty keeps the current value")

	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskUpdateCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	rootCmd.AddCommand(taskCmd)
}
