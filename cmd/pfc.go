package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const pfcKind = "pre_flight_checks"

var pfcCmd = &cobra.Command{
	Use:   "pfc",
	Short: "Pre-flight check operations: read, set, and remove init-time gate commands",
	Long: `Pre-flight checks are shell commands Semaphore runs during pipeline
initialization, before any block starts. They exist at two scopes: one
organization-wide check that applies to every pipeline in the organization, and
one per project. Both run; either can stop a pipeline.

A failing pre-flight check aborts the pipeline before any block is scheduled, so
the symptom is a failed pipeline with no block output.

Scope follows --project: pass it for the project-level check, omit it for the
organization-wide one.`,
}

type pfcScope struct {
	projectID string
}

// resolvePFCScope turns the --project flag into a scope. Unlike most commands,
// an empty flag is not "detect the project from the git remote" — it selects
// the organization-wide check, so nothing is auto-detected here.
func resolvePFCScope(projectFlag string) (pfcScope, error) {
	if strings.TrimSpace(projectFlag) == "" {
		return pfcScope{}, nil
	}
	id, err := resolveProjectID(projectFlag)
	if err != nil {
		return pfcScope{}, err
	}
	return pfcScope{projectID: id}, nil
}

func (s pfcScope) isProject() bool { return s.projectID != "" }

func (s pfcScope) name() string {
	if s.isProject() {
		return "project"
	}
	return "organization"
}

func (s pfcScope) params() url.Values {
	if !s.isProject() {
		return nil
	}
	return url.Values{"project_id": {s.projectID}}
}

func (s pfcScope) permission(action string) string {
	return fmt.Sprintf("%s.pre_flight_checks.%s", s.name(), action)
}

type pfcSpec struct {
	Commands []string  `json:"commands" yaml:"commands"`
	Secrets  []string  `json:"secrets" yaml:"secrets"`
	Agent    *pfcAgent `json:"agent,omitempty" yaml:"agent,omitempty"`
}

type pfcAgent struct {
	MachineType string `json:"machine_type" yaml:"machine_type"`
	OsImage     string `json:"os_image,omitempty" yaml:"os_image,omitempty"`
}

type pfcApplyFlags struct {
	project     string
	commands    []string
	secrets     []string
	machineType string
	osImage     string
	fromFile    string
}

func (f *pfcApplyFlags) hasInlineSpec() bool {
	return len(f.commands) > 0 || len(f.secrets) > 0 || f.machineType != "" || f.osImage != ""
}

func (f *pfcApplyFlags) spec() (*pfcSpec, error) {
	spec, err := f.readSpec()
	if err != nil {
		return nil, err
	}
	if len(spec.Commands) == 0 {
		return nil, fmt.Errorf("a pre-flight check needs at least one command")
	}
	if spec.Secrets == nil {
		spec.Secrets = []string{}
	}
	return spec, nil
}

func (f *pfcApplyFlags) readSpec() (*pfcSpec, error) {
	switch {
	case f.fromFile != "" && f.hasInlineSpec():
		return nil, fmt.Errorf("--from-file cannot be combined with --command/--secret/--machine-type/--os-image; supply the spec one way or the other")
	case f.fromFile != "":
		return loadPFCSpecFile(f.fromFile)
	case !f.hasInlineSpec():
		return nil, fmt.Errorf("no spec given: pass --command (repeatable, plus optional --secret/--machine-type/--os-image) or --from-file <path>")
	}

	spec := &pfcSpec{Commands: f.commands, Secrets: f.secrets}
	if f.machineType != "" || f.osImage != "" {
		spec.Agent = &pfcAgent{MachineType: f.machineType, OsImage: f.osImage}
	}
	return spec, nil
}

// loadPFCSpecFile reads a full spec from a YAML or JSON file. The format is
// sniffed by parsing rather than by extension: JSON first, then YAML, so a
// mis-named file still loads and the reported error is the YAML one (the more
// common format for this file).
func loadPFCSpecFile(path string) (*pfcSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--from-file %s: %w", path, err)
	}

	var spec pfcSpec
	if json.Unmarshal(raw, &spec) == nil {
		return &spec, nil
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("--from-file %s: not valid YAML or JSON: %w", path, err)
	}
	return &spec, nil
}

func pfcServerMessage(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"message", "error"} {
		if s, ok := payload[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func pfcErrorMessage(resp *client.Response, scope pfcScope, action string) string {
	server := pfcServerMessage(resp.Body)
	if strings.Contains(strings.ToLower(server), "not enabled") {
		return server
	}

	switch resp.StatusCode {
	case 404:
		return fmt.Sprintf("no pre-flight check configured for this %s", scope.name())
	case 401, 403:
		return fmt.Sprintf("permission denied: this requires the %s permission", scope.permission(action))
	case 422:
		if server != "" {
			return fmt.Sprintf("invalid pre-flight check: %s", server)
		}
		return fmt.Sprintf("invalid pre-flight check: %s", strings.TrimSpace(string(resp.Body)))
	}
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
}

func pfcFail(resp *client.Response, scope pfcScope, action string) error {
	output.Error("api_error", pfcErrorMessage(resp, scope, action), resp.StatusCode)
	return fmt.Errorf("API returned %d", resp.StatusCode)
}

var pfcShowProjectFlag string

var pfcShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the pre-flight check for the organization, or for one project",
	Long: `Show the pre-flight check — the commands, secrets, and agent used by the init
job that runs before any block in a pipeline.

With --project, shows that project's check; without it, the organization-wide
one. Reports "no pre-flight check configured" when the scope has none.`,
	Args: cobra.NoArgs,
	Example: `  # the organization-wide check
  sem-ai pfc show

  # one project's check
  sem-ai pfc show --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		scope, err := resolvePFCScope(pfcShowProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()
		resp, err := c.ListWithParams(pfcKind, scope.params())
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			return pfcFail(resp, scope, "view")
		}

		var result any
		json.Unmarshal(resp.Body, &result)
		output.Result(result)
		return nil
	},
}

var pfcApply = &pfcApplyFlags{}

var pfcApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Create or replace the pre-flight check — the commands that run before every workflow in the scope",
	Long: `Create or replace the pre-flight check for the organization (default) or for
one project (--project).

This is a privileged change: the commands given here run at the start of every
workflow in that scope, and a non-zero exit stops the pipeline before any block
runs. Show the current check (sem-ai pfc show) and confirm the difference before
applying.

Apply replaces the whole check, it does not merge — the commands, secrets, and
agent sent become the check in full.

The spec comes from flags or from a file, never both:

  flags       --command (repeatable, order preserved), --secret (repeatable),
              --machine-type, --os-image
  --from-file a YAML or JSON file with the same fields:

                commands:
                  - checkout
                  - make security-scan
                secrets:
                  - scanner-token
                agent:
                  machine_type: e2-standard-2
                  os_image: ubuntu2204

Scope always comes from --project; a project_id inside the file is ignored.`,
	Args: cobra.NoArgs,
	Example: `  # organization-wide gate, two commands, one secret
  sem-ai pfc apply --command checkout --command 'make security-scan' --secret scanner-token

  # project-level gate on a specific agent
  sem-ai pfc apply --project my-project \
    --command './scripts/gate.sh' \
    --machine-type e2-standard-2 --os-image ubuntu2204

  # full spec from a file
  sem-ai pfc apply --project my-project --from-file pfc.yml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		spec, err := pfcApply.spec()
		if err != nil {
			output.Error("flag_error", err.Error(), 2)
			return err
		}
		scope, err := resolvePFCScope(pfcApply.project)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		body := map[string]any{"commands": spec.Commands, "secrets": spec.Secrets}
		if spec.Agent != nil {
			body["agent"] = spec.Agent
		}
		if scope.isProject() {
			body["project_id"] = scope.projectID
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			output.Error("format_error", err.Error(), 1)
			return err
		}

		c := client.New()
		resp, err := c.PatchWithParams(pfcKind, nil, bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			return pfcFail(resp, scope, "manage")
		}

		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "applied", "scope": scope.name()})
			return nil
		}
		output.Result(result)
		return nil
	},
}

var pfcDeleteProjectFlag string

var pfcDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove the pre-flight check from the organization, or from one project",
	Long: `Remove the pre-flight check for the organization (default) or for one project
(--project). Pipelines in that scope stop running the check from the next
initialization onwards.

The other scope is untouched: deleting a project's check leaves the
organization-wide one in force for that project.`,
	Args: cobra.NoArgs,
	Example: `  # drop the organization-wide check
  sem-ai pfc delete

  # drop one project's check
  sem-ai pfc delete --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		scope, err := resolvePFCScope(pfcDeleteProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()
		resp, err := c.DeletePathWithParams(pfcKind, scope.params())
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 204 {
			return pfcFail(resp, scope, "manage")
		}

		result := map[string]string{"status": "deleted", "scope": scope.name()}
		if scope.isProject() {
			result["project_id"] = scope.projectID
		}
		output.Result(result)
		return nil
	},
}

func init() {
	pfcShowCmd.Flags().StringVar(&pfcShowProjectFlag, "project", "", "project name or ID; omit for the organization-wide check")
	pfcDeleteCmd.Flags().StringVar(&pfcDeleteProjectFlag, "project", "", "project name or ID; omit for the organization-wide check")

	pfcApplyCmd.Flags().StringVar(&pfcApply.project, "project", "", "project name or ID; omit for the organization-wide check")
	pfcApplyCmd.Flags().StringArrayVar(&pfcApply.commands, "command", nil, "shell command to run at pipeline init; repeatable, order preserved")
	pfcApplyCmd.Flags().StringArrayVar(&pfcApply.secrets, "secret", nil, "name of a secret to expose to the commands; repeatable")
	pfcApplyCmd.Flags().StringVar(&pfcApply.machineType, "machine-type", "", "agent machine type for the init job (e.g. e2-standard-2)")
	pfcApplyCmd.Flags().StringVar(&pfcApply.osImage, "os-image", "", "agent OS image for the init job (e.g. ubuntu2204)")
	pfcApplyCmd.Flags().StringVar(&pfcApply.fromFile, "from-file", "", "path to a YAML or JSON file holding the full spec; mutually exclusive with the spec flags")

	pfcCmd.AddCommand(pfcShowCmd)
	pfcCmd.AddCommand(pfcApplyCmd)
	pfcCmd.AddCommand(pfcDeleteCmd)
	rootCmd.AddCommand(pfcCmd)
}
