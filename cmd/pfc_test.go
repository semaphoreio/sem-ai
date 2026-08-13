package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/semaphoreio/sem-ai/pkg/client"
)

// pfcMock wires the shared API mock and clears the pfc flag vars, which persist
// between tests because RunE is called directly (no cobra flag reset).
func pfcMock(t *testing.T, handler http.HandlerFunc) (reqs *[]capturedReq, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetPFCFlags()
	t.Cleanup(resetPFCFlags)
	return apiMock(t, handler)
}

func resetPFCFlags() {
	pfcShowProjectFlag, pfcDeleteProjectFlag = "", ""
	*pfcApply = pfcApplyFlags{}
}

// writeSpecFile drops a --from-file fixture in a temp dir and returns its path.
func writeSpecFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// decodeError parses the structured error the output package wrote to stderr.
func decodeError(t *testing.T, stderr *bytes.Buffer) map[string]any {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &e); err != nil {
		t.Fatalf("stderr is not a structured error: %v (got %q)", err, stderr.String())
	}
	return e
}

// projectLookupHandler answers the resolveProjectID lookup for one project name.
func projectLookupHandler(t *testing.T, name, id string, then http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/"+name {
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": id, "name": name}})
			return
		}
		then(w, r)
	}
}

// ---- spec resolution: flags, --from-file, and the two ambiguous cases -------

func TestPFCSpec_FlagsOnly(t *testing.T) {
	f := &pfcApplyFlags{
		commands:    []string{"checkout", "make security-scan"},
		secrets:     []string{"scanner-token"},
		machineType: "e2-standard-2",
		osImage:     "ubuntu2204",
	}

	spec, err := f.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if len(spec.Commands) != 2 || spec.Commands[0] != "checkout" || spec.Commands[1] != "make security-scan" {
		t.Errorf("commands = %v, want [checkout, make security-scan] in order", spec.Commands)
	}
	if len(spec.Secrets) != 1 || spec.Secrets[0] != "scanner-token" {
		t.Errorf("secrets = %v, want [scanner-token]", spec.Secrets)
	}
	if spec.Agent == nil {
		t.Fatal("agent = nil, want machine type and OS image")
	}
	if spec.Agent.MachineType != "e2-standard-2" || spec.Agent.OsImage != "ubuntu2204" {
		t.Errorf("agent = %+v, want e2-standard-2/ubuntu2204", *spec.Agent)
	}
}

func TestPFCSpec_FlagsWithoutAgentOmitsAgent(t *testing.T) {
	f := &pfcApplyFlags{commands: []string{"./gate.sh"}}

	spec, err := f.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if spec.Agent != nil {
		t.Errorf("agent = %+v, want nil so the server picks its default", *spec.Agent)
	}
	if spec.Secrets == nil {
		t.Error("secrets = nil, want an empty slice so apply clears previously bound secrets")
	}
}

func TestPFCSpec_FromFileYAML(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", `commands:
  - checkout
  - make security-scan
secrets:
  - scanner-token
agent:
  machine_type: e2-standard-2
  os_image: ubuntu2204
`)
	f := &pfcApplyFlags{fromFile: path}

	spec, err := f.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if len(spec.Commands) != 2 || spec.Commands[1] != "make security-scan" {
		t.Errorf("commands = %v, want [checkout, make security-scan]", spec.Commands)
	}
	if len(spec.Secrets) != 1 || spec.Secrets[0] != "scanner-token" {
		t.Errorf("secrets = %v, want [scanner-token]", spec.Secrets)
	}
	if spec.Agent == nil || spec.Agent.MachineType != "e2-standard-2" || spec.Agent.OsImage != "ubuntu2204" {
		t.Errorf("agent = %+v, want e2-standard-2/ubuntu2204", spec.Agent)
	}
}

func TestPFCSpec_FromFileJSON(t *testing.T) {
	path := writeSpecFile(t, "pfc.json", `{
  "commands": ["checkout", "make security-scan"],
  "secrets": ["scanner-token"],
  "agent": {"machine_type": "e1-standard-4", "os_image": "ubuntu2004"}
}`)
	f := &pfcApplyFlags{fromFile: path}

	spec, err := f.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if len(spec.Commands) != 2 || spec.Commands[0] != "checkout" {
		t.Errorf("commands = %v, want [checkout, make security-scan]", spec.Commands)
	}
	if spec.Agent == nil || spec.Agent.MachineType != "e1-standard-4" || spec.Agent.OsImage != "ubuntu2004" {
		t.Errorf("agent = %+v, want e1-standard-4/ubuntu2004", spec.Agent)
	}
}

func TestPFCSpec_FromFileAndFlagsIsAnError(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", "commands:\n  - checkout\n")
	f := &pfcApplyFlags{fromFile: path, commands: []string{"./gate.sh"}}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error when both --from-file and spec flags are given")
	}
	if !strings.Contains(err.Error(), "--from-file cannot be combined") {
		t.Errorf("error = %q, want it to name the conflict", err)
	}
}

func TestPFCSpec_NeitherFileNorFlagsIsAnError(t *testing.T) {
	f := &pfcApplyFlags{project: "my-project"}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error when no spec is given")
	}
	for _, want := range []string{"--command", "--from-file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %s", err, want)
		}
	}
}

func TestPFCSpec_AgentFlagsWithoutCommandIsAnError(t *testing.T) {
	f := &pfcApplyFlags{machineType: "e2-standard-2"}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error: an agent with no commands is not a check")
	}
	if !strings.Contains(err.Error(), "at least one command") {
		t.Errorf("error = %q, want it to ask for a command", err)
	}
}

func TestPFCSpec_FileWithNoCommandsIsAnError(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", "secrets:\n  - scanner-token\n")
	f := &pfcApplyFlags{fromFile: path}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error for a file with no commands")
	}
	if !strings.Contains(err.Error(), "at least one command") {
		t.Errorf("error = %q, want it to ask for a command", err)
	}
}

func TestPFCSpec_MissingFileIsAnError(t *testing.T) {
	f := &pfcApplyFlags{fromFile: filepath.Join(t.TempDir(), "absent.yml")}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error for a missing --from-file path")
	}
	if !strings.Contains(err.Error(), "--from-file") {
		t.Errorf("error = %q, want it to name the flag", err)
	}
}

func TestPFCSpec_UnparseableFileIsAnError(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", "commands:\n\t- broken tab indent\n")
	f := &pfcApplyFlags{fromFile: path}

	_, err := f.spec()
	if err == nil {
		t.Fatal("expected an error for a file that is neither YAML nor JSON")
	}
	if !strings.Contains(err.Error(), "not valid YAML or JSON") {
		t.Errorf("error = %q, want it to say the file could not be parsed", err)
	}
}

// ---- scope: --project present or absent shapes the request ------------------

func TestPFCShow_OrganizationScopeSendsNoProjectID(t *testing.T) {
	reqs, stdout, _ := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"commands":     []string{"checkout"},
			"secrets":      []string{},
			"requester_id": "u1",
		})
	})

	if err := pfcShowCmd.RunE(pfcShowCmd, nil); err != nil {
		t.Fatalf("pfc show: %v", err)
	}

	get := find(t, reqs, "GET", "/api/v1alpha/pre_flight_checks")
	if len(get.Query) != 0 {
		t.Errorf("query = %v, want none for organization scope", get.Query)
	}
	if !strings.Contains(stdout.String(), "checkout") {
		t.Errorf("stdout = %q, want the check body", stdout.String())
	}
}

func TestPFCShow_ProjectScopeResolvesNameToID(t *testing.T) {
	reqs, _, _ := pfcMock(t, projectLookupHandler(t, "my-project", "proj-uuid",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"commands": []string{"checkout"}})
		}))

	pfcShowProjectFlag = "my-project"
	if err := pfcShowCmd.RunE(pfcShowCmd, nil); err != nil {
		t.Fatalf("pfc show: %v", err)
	}

	get := find(t, reqs, "GET", "/api/v1alpha/pre_flight_checks")
	if got := get.Query.Get("project_id"); got != "proj-uuid" {
		t.Errorf("project_id = %q, want the resolved UUID proj-uuid", got)
	}
}

func TestPFCApply_OrganizationScopePatchesFullSpec(t *testing.T) {
	reqs, _, _ := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"commands": []string{"checkout"}})
	})

	pfcApply.commands = []string{"checkout", "make security-scan"}
	pfcApply.secrets = []string{"scanner-token"}
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err != nil {
		t.Fatalf("pfc apply: %v", err)
	}

	patch := find(t, reqs, "PATCH", "/api/v1alpha/pre_flight_checks")
	if got := asStrings(patch.Body["commands"]); len(got) != 2 || got[0] != "checkout" {
		t.Errorf("commands = %v, want [checkout, make security-scan]", got)
	}
	if got := asStrings(patch.Body["secrets"]); len(got) != 1 || got[0] != "scanner-token" {
		t.Errorf("secrets = %v, want [scanner-token]", got)
	}
	if _, sent := patch.Body["project_id"]; sent {
		t.Errorf("project_id = %v, want it absent for organization scope", patch.Body["project_id"])
	}
	if _, sent := patch.Body["agent"]; sent {
		t.Errorf("agent = %v, want it absent when no agent flags are given", patch.Body["agent"])
	}
}

func TestPFCApply_ProjectScopeSendsProjectIDAndAgentInBody(t *testing.T) {
	reqs, _, _ := pfcMock(t, projectLookupHandler(t, "my-project", "proj-uuid",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"commands": []string{"./gate.sh"}})
		}))

	pfcApply.project = "my-project"
	pfcApply.commands = []string{"./gate.sh"}
	pfcApply.machineType = "e2-standard-2"
	pfcApply.osImage = "ubuntu2204"
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err != nil {
		t.Fatalf("pfc apply: %v", err)
	}

	patch := find(t, reqs, "PATCH", "/api/v1alpha/pre_flight_checks")
	if patch.Body["project_id"] != "proj-uuid" {
		t.Errorf("project_id = %v, want proj-uuid", patch.Body["project_id"])
	}
	agent, ok := patch.Body["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent = %v, want an object", patch.Body["agent"])
	}
	if agent["machine_type"] != "e2-standard-2" || agent["os_image"] != "ubuntu2204" {
		t.Errorf("agent = %v, want e2-standard-2/ubuntu2204", agent)
	}
	if got := asStrings(patch.Body["secrets"]); got == nil || len(got) != 0 {
		t.Errorf("secrets = %v, want an empty array so apply clears bound secrets", patch.Body["secrets"])
	}
}

func TestPFCApply_FromFileIsSentAsTheBody(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", `commands:
  - checkout
  - ./scripts/gate.sh
secrets:
  - gate-token
`)
	reqs, _, _ := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"commands": []string{"checkout"}})
	})

	pfcApply.fromFile = path
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err != nil {
		t.Fatalf("pfc apply: %v", err)
	}

	patch := find(t, reqs, "PATCH", "/api/v1alpha/pre_flight_checks")
	if got := asStrings(patch.Body["commands"]); len(got) != 2 || got[1] != "./scripts/gate.sh" {
		t.Errorf("commands = %v, want the two commands from the file", got)
	}
	if got := asStrings(patch.Body["secrets"]); len(got) != 1 || got[0] != "gate-token" {
		t.Errorf("secrets = %v, want [gate-token]", got)
	}
}

func TestPFCApply_ConflictingInputNeverReachesTheAPI(t *testing.T) {
	path := writeSpecFile(t, "pfc.yml", "commands:\n  - checkout\n")
	reqs, _, stderr := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{})
	})

	pfcApply.fromFile = path
	pfcApply.commands = []string{"./gate.sh"}
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err == nil {
		t.Fatal("expected pfc apply to fail when both input modes are used")
	}

	if n := count(reqs, "PATCH", "/api/v1alpha/pre_flight_checks"); n != 0 {
		t.Errorf("%d PATCH requests, want 0 — bad input must not reach the API", n)
	}
	if got := decodeError(t, stderr)["code"]; got != "flag_error" {
		t.Errorf("error code = %v, want flag_error", got)
	}
}

func TestPFCDelete_OrganizationAndProjectScope(t *testing.T) {
	t.Run("organization", func(t *testing.T) {
		reqs, stdout, _ := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{})
		})

		if err := pfcDeleteCmd.RunE(pfcDeleteCmd, nil); err != nil {
			t.Fatalf("pfc delete: %v", err)
		}

		del := find(t, reqs, "DELETE", "/api/v1alpha/pre_flight_checks")
		if len(del.Query) != 0 {
			t.Errorf("query = %v, want none for organization scope", del.Query)
		}
		if !strings.Contains(stdout.String(), `"organization"`) {
			t.Errorf("stdout = %q, want it to name the scope deleted", stdout.String())
		}
	})

	t.Run("project", func(t *testing.T) {
		reqs, stdout, _ := pfcMock(t, projectLookupHandler(t, "my-project", "proj-uuid",
			func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{})
			}))

		pfcDeleteProjectFlag = "my-project"
		if err := pfcDeleteCmd.RunE(pfcDeleteCmd, nil); err != nil {
			t.Fatalf("pfc delete: %v", err)
		}

		del := find(t, reqs, "DELETE", "/api/v1alpha/pre_flight_checks")
		if got := del.Query.Get("project_id"); got != "proj-uuid" {
			t.Errorf("project_id = %q, want proj-uuid", got)
		}
		if !strings.Contains(stdout.String(), "proj-uuid") {
			t.Errorf("stdout = %q, want it to name the project deleted", stdout.String())
		}
	})
}

// ---- error mapping ----------------------------------------------------------

func TestPFCErrorMessage(t *testing.T) {
	org := pfcScope{}
	project := pfcScope{projectID: "proj-uuid"}

	tests := []struct {
		name   string
		resp   *client.Response
		scope  pfcScope
		action string
		want   string
	}{
		{
			name:   "404 organization",
			resp:   &client.Response{StatusCode: 404, Body: []byte(`{"message":"Not Found"}`)},
			scope:  org,
			action: "view",
			want:   "no pre-flight check configured for this organization",
		},
		{
			name:   "404 project",
			resp:   &client.Response{StatusCode: 404, Body: []byte(``)},
			scope:  project,
			action: "view",
			want:   "no pre-flight check configured for this project",
		},
		{
			name:   "401 names the organization manage permission",
			resp:   &client.Response{StatusCode: 401, Body: []byte(`{"message":"Unauthorized"}`)},
			scope:  org,
			action: "manage",
			want:   "permission denied: this requires the organization.pre_flight_checks.manage permission",
		},
		{
			name:   "401 names the project manage permission",
			resp:   &client.Response{StatusCode: 401, Body: []byte(`{}`)},
			scope:  project,
			action: "manage",
			want:   "permission denied: this requires the project.pre_flight_checks.manage permission",
		},
		{
			name:   "422 quotes the server's complaint",
			resp:   &client.Response{StatusCode: 422, Body: []byte(`{"message":"commands cannot be empty"}`)},
			scope:  org,
			action: "manage",
			want:   "invalid pre-flight check: commands cannot be empty",
		},
		{
			name:   "feature disabled is passed through verbatim",
			resp:   &client.Response{StatusCode: 404, Body: []byte(`{"message":"pre_flight_checks feature is not enabled for your organization"}`)},
			scope:  org,
			action: "view",
			want:   "pre_flight_checks feature is not enabled for your organization",
		},
		{
			name:   "unmapped status falls back to the raw response",
			resp:   &client.Response{StatusCode: 418, Body: []byte(`teapot`)},
			scope:  org,
			action: "view",
			want:   "HTTP 418: teapot",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pfcErrorMessage(tc.resp, tc.scope, tc.action); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPFCShow_NotConfiguredReportsAnActionableError(t *testing.T) {
	_, _, stderr := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"message": "Not Found"})
	})

	if err := pfcShowCmd.RunE(pfcShowCmd, nil); err == nil {
		t.Fatal("expected pfc show to fail on 404")
	}

	e := decodeError(t, stderr)
	if e["code"] != "api_error" {
		t.Errorf("code = %v, want api_error", e["code"])
	}
	if e["message"] != "no pre-flight check configured for this organization" {
		t.Errorf("message = %v, want the not-configured wording", e["message"])
	}
	if e["status"] != float64(404) {
		t.Errorf("status = %v, want 404", e["status"])
	}
}

func TestPFCApply_PermissionDeniedNamesThePermission(t *testing.T) {
	_, _, stderr := pfcMock(t, projectLookupHandler(t, "my-project", "proj-uuid",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 401, map[string]any{"message": "Unauthorized"})
		}))

	pfcApply.project = "my-project"
	pfcApply.commands = []string{"./gate.sh"}
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err == nil {
		t.Fatal("expected pfc apply to fail on 401")
	}

	msg, _ := decodeError(t, stderr)["message"].(string)
	if !strings.Contains(msg, "project.pre_flight_checks.manage") {
		t.Errorf("message = %q, want it to name the manage permission", msg)
	}
}

func TestPFCApply_FeatureDisabledIsSurfacedVerbatim(t *testing.T) {
	const serverMsg = "pre_flight_checks feature is not enabled for your organization"
	_, _, stderr := pfcMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"message": serverMsg})
	})

	pfcApply.commands = []string{"./gate.sh"}
	if err := pfcApplyCmd.RunE(pfcApplyCmd, nil); err == nil {
		t.Fatal("expected pfc apply to fail when the feature is disabled")
	}

	if got := decodeError(t, stderr)["message"]; got != serverMsg {
		t.Errorf("message = %v, want the server's own wording %q", got, serverMsg)
	}
}

// ---- MCP: the cobra tree walk picks these up without registration code ------

func TestPFCCommandsRegisterAsMCPTools(t *testing.T) {
	s := server.NewMCPServer("sem-ai", "test")
	registerCobraTools(s, rootCmd, "")

	tools := s.ListTools()
	for _, name := range []string{"pfc_show", "pfc_apply", "pfc_delete"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q not registered; got %d tools", name, len(tools))
		}
		if tool.Tool.Description == "" {
			t.Errorf("tool %q has no description for the calling agent", name)
		}
		if _, ok := tool.Tool.InputSchema.Properties["project"]; !ok {
			t.Errorf("tool %q has no project parameter", name)
		}
	}

	if _, ok := tools["pfc"]; ok {
		t.Error("the pfc group itself should not be a tool, only its leaves")
	}

	apply := tools["pfc_apply"]
	for _, param := range []string{"command", "secret", "machine-type", "os-image", "from-file"} {
		if _, ok := apply.Tool.InputSchema.Properties[param]; !ok {
			t.Errorf("pfc_apply is missing the %q parameter", param)
		}
	}
	if !strings.Contains(apply.Tool.Description, "before every workflow") {
		t.Errorf("pfc_apply description = %q, want it to state what applying changes", apply.Tool.Description)
	}
}
