package cmd

import (
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseParamDefs(t *testing.T) {
	cases := []struct {
		in   []string
		want []taskParamDef
		err  bool
	}{
		{nil, []taskParamDef{}, false},
		{[]string{"VERSION"}, []taskParamDef{{Name: "VERSION", Required: true}}, false},
		{[]string{"ENVIRONMENT=staging"}, []taskParamDef{{Name: "ENVIRONMENT", Required: false, DefaultValue: "staging"}}, false},
		{[]string{"EMPTY_DEFAULT="}, []taskParamDef{{Name: "EMPTY_DEFAULT", Required: false, DefaultValue: ""}}, false},
		{[]string{"WITH_EQ=a=b"}, []taskParamDef{{Name: "WITH_EQ", Required: false, DefaultValue: "a=b"}}, false},
		{[]string{"A", "B=x"}, []taskParamDef{{Name: "A", Required: true}, {Name: "B", Required: false, DefaultValue: "x"}}, false},
		{[]string{"=oops"}, nil, true},
		{[]string{""}, nil, true},
	}
	for _, tc := range cases {
		got, err := parseParamDefs(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%v: want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: unexpected error: %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%v: got %d defs, want %d", tc.in, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%v[%d]: got %+v, want %+v", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestBuildScheduleYAMLWithParams(t *testing.T) {
	yml := buildScheduleYAML("nightly", "my-app", "main", ".semaphore/nightly.yml", "0 2 * * *", false, false, []taskParamDef{
		{Name: "VERSION", Required: true},
		{Name: "ENVIRONMENT", Required: false, DefaultValue: "staging"},
	})
	for _, want := range []string{
		"apiVersion: v1.1\n",
		"kind: Periodic\n",
		"  recurring: true\n",
		"  at: \"0 2 * * *\"\n",
		"  parameters:\n",
		"    - name: VERSION\n      required: true\n",
		"    - name: ENVIRONMENT\n      required: false\n      default_value: staging\n",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("YAML missing %q:\n%s", want, yml)
		}
	}
}

func TestBuildScheduleYAMLNoParams(t *testing.T) {
	yml := buildScheduleYAML("oneoff", "my-app", "main", ".semaphore/run.yml", "", false, false, nil)
	if strings.Contains(yml, "parameters:") {
		t.Errorf("unexpected parameters block:\n%s", yml)
	}
	if !strings.Contains(yml, "recurring: false") {
		t.Errorf("expected recurring: false:\n%s", yml)
	}
	// Unset flags must be omitted, not emitted as false: the v1.1 apply
	// endpoint treats an omitted key as "no change".
	if strings.Contains(yml, "skip_scheduled_run_notifications") ||
		strings.Contains(yml, "skip_manual_run_notifications") {
		t.Errorf("unexpected notification skip keys:\n%s", yml)
	}
}

func TestBuildScheduleYAMLNotificationSkipFlags(t *testing.T) {
	cases := []struct {
		skipScheduled, skipManual bool
	}{
		{true, false},
		{false, true},
		{true, true},
	}
	for _, tc := range cases {
		yml := buildScheduleYAML("quiet", "my-app", "main", ".semaphore/cron.yml", "0 2 * * *",
			tc.skipScheduled, tc.skipManual, nil)

		if got := strings.Contains(yml, "  skip_scheduled_run_notifications: true\n"); got != tc.skipScheduled {
			t.Errorf("skipScheduled=%t: scheduled key present=%t:\n%s", tc.skipScheduled, got, yml)
		}
		if got := strings.Contains(yml, "  skip_manual_run_notifications: true\n"); got != tc.skipManual {
			t.Errorf("skipManual=%t: manual key present=%t:\n%s", tc.skipManual, got, yml)
		}
	}
}

func TestWithNotificationSkipFlags(t *testing.T) {
	t.Run("missing flags default to false", func(t *testing.T) {
		got := withNotificationSkipFlags(map[string]any{
			"schedule": map[string]any{"id": "t1"},
		})
		schedule := got.(map[string]any)["schedule"].(map[string]any)
		for _, k := range taskNotificationSkipKeys {
			if v, ok := schedule[k]; !ok || v != false {
				t.Errorf("%s = %v (present=%t), want false", k, v, ok)
			}
		}
	})

	t.Run("server values are kept", func(t *testing.T) {
		got := withNotificationSkipFlags(map[string]any{
			"schedule": map[string]any{
				"skip_scheduled_run_notifications": true,
				"skip_manual_run_notifications":    false,
			},
		})
		schedule := got.(map[string]any)["schedule"].(map[string]any)
		if schedule["skip_scheduled_run_notifications"] != true {
			t.Errorf("scheduled flag overwritten: %v", schedule)
		}
		if schedule["skip_manual_run_notifications"] != false {
			t.Errorf("manual flag overwritten: %v", schedule)
		}
	})

	t.Run("unexpected shapes pass through", func(t *testing.T) {
		for _, in := range []any{nil, "oops", []any{1}, map[string]any{"error": "x"}} {
			got := withNotificationSkipFlags(in)
			if m, ok := got.(map[string]any); ok {
				if _, has := m["schedule"]; has {
					t.Errorf("schedule injected into %v", in)
				}
			}
		}
	})
}

// taskUpdateMock serves GET /tasks/t1 with schedule, resolves proj-1 to
// my-app, and accepts the apply POST.
func taskUpdateMock(t *testing.T, schedule map[string]any) *[]capturedReq {
	t.Helper()
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/tasks/t1":
			writeJSON(w, 200, map[string]any{"schedule": schedule, "triggers": []any{}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-1":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-1", "name": "my-app"}})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/tasks":
			writeJSON(w, 200, map[string]any{"id": "t1"})
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})
	t.Cleanup(func() { taskUpdateSkipScheduledFlag, taskUpdateSkipManualFlag = "", "" })
	return reqs
}

// postedSpec decodes the yml_definition of the recorded apply POST.
func postedSpec(t *testing.T, reqs *[]capturedReq) (metadata, spec map[string]any) {
	t.Helper()
	post := find(t, reqs, "POST", "/api/v1alpha/tasks")
	raw, _ := post.Body["yml_definition"].(string)
	var doc struct {
		Metadata map[string]any `yaml:"metadata"`
		Spec     map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("posted yml_definition is not valid YAML: %v\n%s", err, raw)
	}
	return doc.Metadata, doc.Spec
}

func TestTaskUpdate_ChangesOnlyRequestedFlag(t *testing.T) {
	reqs := taskUpdateMock(t, map[string]any{
		"id":                               "t1",
		"name":                             "nightly",
		"project_id":                       "proj-1",
		"branch":                           "main",
		"reference":                        "refs/heads/main",
		"at":                               "0 2 * * *",
		"pipeline_file":                    ".semaphore/cron.yml",
		"recurring":                        true,
		"paused":                           true,
		"description":                      "keep me",
		"parameters":                       []any{map[string]any{"name": "ENV", "required": true, "options": []any{"a", "b"}}},
		"skip_scheduled_run_notifications": true,
		"skip_manual_run_notifications":    false,
	})

	taskUpdateSkipManualFlag = "true"
	if err := taskUpdateCmd.RunE(taskUpdateCmd, []string{"t1"}); err != nil {
		t.Fatalf("task update: %v", err)
	}

	metadata, spec := postedSpec(t, reqs)
	if metadata["id"] != "t1" || metadata["name"] != "nightly" {
		t.Errorf("metadata = %v, want id t1 and name nightly", metadata)
	}
	want := map[string]any{
		"project":                       "my-app",
		"branch":                        "main",
		"at":                            "0 2 * * *",
		"pipeline_file":                 ".semaphore/cron.yml",
		"recurring":                     true,
		"skip_manual_run_notifications": true,
	}
	for k, v := range want {
		if spec[k] != v {
			t.Errorf("spec.%s = %v, want %v", k, spec[k], v)
		}
	}
	// Omitted keys are "no change" on apply's update path: the stored
	// scheduled flag, parameters and paused state must survive untouched.
	for _, k := range []string{"skip_scheduled_run_notifications", "parameters", "paused", "description"} {
		if _, ok := spec[k]; ok {
			t.Errorf("spec.%s should be omitted so the server keeps it; got %v", k, spec[k])
		}
	}
}

func TestTaskUpdate_FalseIsSentExplicitly(t *testing.T) {
	reqs := taskUpdateMock(t, map[string]any{
		"id": "t1", "name": "nightly", "project_id": "proj-1", "branch": "main",
		"reference": "refs/heads/main", "at": "0 2 * * *", "pipeline_file": ".semaphore/cron.yml",
		"recurring": true, "skip_scheduled_run_notifications": true,
	})

	taskUpdateSkipScheduledFlag = "false"
	if err := taskUpdateCmd.RunE(taskUpdateCmd, []string{"t1"}); err != nil {
		t.Fatalf("task update: %v", err)
	}

	_, spec := postedSpec(t, reqs)
	if v, ok := spec["skip_scheduled_run_notifications"]; !ok || v != false {
		t.Errorf("skip_scheduled_run_notifications = %v (present=%t), want explicit false", v, ok)
	}
	if _, ok := spec["skip_manual_run_notifications"]; ok {
		t.Errorf("unchanged manual flag should be omitted")
	}
}

func TestTaskUpdate_NonBranchReferenceAndOneOff(t *testing.T) {
	reqs := taskUpdateMock(t, map[string]any{
		"id": "t1", "name": "release", "project_id": "proj-1",
		"reference": "refs/tags/v1.0.0", "pipeline_file": ".semaphore/release.yml",
		"recurring": false,
	})

	taskUpdateSkipManualFlag = "true"
	if err := taskUpdateCmd.RunE(taskUpdateCmd, []string{"t1"}); err != nil {
		t.Fatalf("task update: %v", err)
	}

	_, spec := postedSpec(t, reqs)
	if spec["branch"] != "refs/tags/v1.0.0" {
		t.Errorf("branch = %v, want full tag ref", spec["branch"])
	}
	if spec["recurring"] != false {
		t.Errorf("recurring = %v, want false", spec["recurring"])
	}
	if _, ok := spec["at"]; ok {
		t.Errorf("one-off task should not send at; got %v", spec["at"])
	}
}

func TestTaskUpdate_RejectsBadInputBeforeAnyRequest(t *testing.T) {
	cases := []struct{ scheduled, manual string }{
		{"", ""},      // nothing to update
		{"yes", ""},   // not true/false
		{"", "maybe"}, // not true/false
	}
	for _, tc := range cases {
		reqs := taskUpdateMock(t, map[string]any{"id": "t1"})
		taskUpdateSkipScheduledFlag, taskUpdateSkipManualFlag = tc.scheduled, tc.manual
		if err := taskUpdateCmd.RunE(taskUpdateCmd, []string{"t1"}); err == nil {
			t.Errorf("scheduled=%q manual=%q: expected error", tc.scheduled, tc.manual)
		}
		if len(*reqs) != 0 {
			t.Errorf("scheduled=%q manual=%q: made %d requests, want 0", tc.scheduled, tc.manual, len(*reqs))
		}
	}
}

func TestTaskUpdate_ApplyErrorIsReturned(t *testing.T) {
	apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/tasks/t1":
			writeJSON(w, 200, map[string]any{"schedule": map[string]any{
				"id": "t1", "name": "nightly", "project_id": "proj-1", "branch": "main",
				"at": "0 2 * * *", "pipeline_file": ".semaphore/cron.yml", "recurring": true,
			}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-1":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-1", "name": "my-app"}})
		default:
			writeJSON(w, 400, map[string]any{"message": "unknown key skip_manual_run_notifications"})
		}
	})
	t.Cleanup(func() { taskUpdateSkipManualFlag = "" })

	taskUpdateSkipManualFlag = "true"
	if err := taskUpdateCmd.RunE(taskUpdateCmd, []string{"t1"}); err == nil {
		t.Fatal("expected error when apply returns 400")
	}
}

func TestParseOptionalBool(t *testing.T) {
	for in, want := range map[string]string{"": "nil", "true": "true", " TRUE ": "true", "false": "false", "False": "false"} {
		got, err := parseOptionalBool("f", in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		gotS := "nil"
		if got != nil {
			gotS = map[bool]string{true: "true", false: "false"}[*got]
		}
		if gotS != want {
			t.Errorf("%q = %s, want %s", in, gotS, want)
		}
	}
	if _, err := parseOptionalBool("f", "1"); err == nil {
		t.Error("expected error for 1")
	}
}

// Over MCP, update flags arrive as strings, so an explicit "false" reaches
// the command (a bool flag would be dropped by toolCLIArgs).
func TestTaskUpdate_MCPForwardsFalse(t *testing.T) {
	argv := toolCLIArgs(taskUpdateCmd, map[string]any{
		"args":                             "t1",
		"skip-scheduled-run-notifications": "false",
	})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "t1") || !strings.Contains(joined, "--skip-scheduled-run-notifications false") {
		t.Errorf("argv = %v, want task id and explicit false", argv)
	}
}
