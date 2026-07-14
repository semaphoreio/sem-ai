package cmd

import (
	"strings"
	"testing"
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
	yml := buildScheduleYAML("nightly", "my-app", "main", ".semaphore/nightly.yml", "0 2 * * *", []taskParamDef{
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
	yml := buildScheduleYAML("oneoff", "my-app", "main", ".semaphore/run.yml", "", nil)
	if strings.Contains(yml, "parameters:") {
		t.Errorf("unexpected parameters block:\n%s", yml)
	}
	if !strings.Contains(yml, "recurring: false") {
		t.Errorf("expected recurring: false:\n%s", yml)
	}
}
