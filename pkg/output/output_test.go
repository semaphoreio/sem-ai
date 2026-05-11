package output

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ---- SetWriters redirects --------------------------------------------------------

func TestSetWritersRedirectsStdout(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil) // restore

	SetFormat("json")
	Result(map[string]string{"key": "value"})

	got := buf.String()
	if !strings.Contains(got, `"key"`) {
		t.Errorf("expected output to contain key, got: %q", got)
	}
}

func TestSetWritersRedirectsStderr(t *testing.T) {
	var errBuf bytes.Buffer
	SetWriters(nil, &errBuf)
	defer SetWriters(nil, nil)

	Error("test_code", "something went wrong", 500)

	got := errBuf.String()
	if !strings.Contains(got, "something went wrong") {
		t.Errorf("expected stderr to contain error message, got: %q", got)
	}
}

func TestSetWritersNilResetsToDefaults(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, &buf)

	// Reset to defaults
	SetWriters(nil, nil)

	// After reset, stdout/stderr should be os.Stdout/os.Stderr (not &buf)
	if stdout != os.Stdout {
		t.Error("stdout should be reset to os.Stdout")
	}
	if stderr != os.Stderr {
		t.Error("stderr should be reset to os.Stderr")
	}
}

func TestSetWritersBothNil(t *testing.T) {
	// Override first
	var buf bytes.Buffer
	SetWriters(&buf, &buf)

	// Reset both at once
	SetWriters(nil, nil)

	if stdout == &buf {
		t.Error("stdout was not reset")
	}
	if stderr == &buf {
		t.Error("stderr was not reset")
	}
}

// ---- JSON output ----------------------------------------------------------------

func TestResultJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("json")
	Result(map[string]any{"name": "project-a", "count": 3})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %q", err, buf.String())
	}
	if parsed["name"] != "project-a" {
		t.Errorf("name = %v, want project-a", parsed["name"])
	}
}

func TestResultJSONIndented(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("json")
	Result(map[string]string{"a": "b"})

	// json.MarshalIndent produces multi-line output
	got := buf.String()
	if !strings.Contains(got, "\n") {
		t.Error("expected indented (multi-line) JSON output")
	}
}

func TestResultJSONArray(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("json")
	Result([]map[string]string{{"id": "1"}, {"id": "2"}})

	var arr []map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &arr); err != nil {
		t.Fatalf("output is not valid JSON array: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 elements, got %d", len(arr))
	}
}

// ---- YAML output ----------------------------------------------------------------

func TestResultYAMLFormat(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("yaml")
	Result(map[string]string{"status": "ok"})

	got := buf.String()
	if !strings.Contains(got, "status: ok") {
		t.Errorf("expected YAML output with 'status: ok', got: %q", got)
	}
}

// ---- Table output ---------------------------------------------------------------

func TestResultTableFormat(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("table")
	Result([]map[string]any{
		{"id": "1", "name": "alpha"},
		{"id": "2", "name": "beta"},
	})

	got := buf.String()
	// Table should contain column headers and values
	if !strings.Contains(got, "id") {
		t.Errorf("expected table with 'id' column, got: %q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Errorf("expected table row with 'alpha', got: %q", got)
	}
}

func TestResultTableSingleObject(t *testing.T) {
	var buf bytes.Buffer
	SetWriters(&buf, nil)
	defer SetWriters(nil, nil)

	SetFormat("table")
	Result(map[string]any{"key": "value", "num": 42})

	got := buf.String()
	if !strings.Contains(got, "key") {
		t.Errorf("expected 'key' in table output, got: %q", got)
	}
}

// ---- Error output ---------------------------------------------------------------

func TestErrorOutputFormat(t *testing.T) {
	var errBuf bytes.Buffer
	SetWriters(nil, &errBuf)
	defer SetWriters(nil, nil)

	Error("not_found", "project does not exist", 404)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(errBuf.String())), &parsed); err != nil {
		t.Fatalf("error output is not valid JSON: %v\noutput: %q", err, errBuf.String())
	}

	if parsed["error"] != true {
		t.Errorf("error field = %v, want true", parsed["error"])
	}
	if parsed["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", parsed["code"])
	}
	if parsed["message"] != "project does not exist" {
		t.Errorf("message = %v, want 'project does not exist'", parsed["message"])
	}
	// status comes back as float64 from JSON
	if parsed["status"].(float64) != 404 {
		t.Errorf("status = %v, want 404", parsed["status"])
	}
}

func TestErrorGoesToStderr(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	SetWriters(&outBuf, &errBuf)
	defer SetWriters(nil, nil)

	Error("err_code", "msg", 1)

	if outBuf.Len() != 0 {
		t.Errorf("stdout should be empty, got: %q", outBuf.String())
	}
	if errBuf.Len() == 0 {
		t.Error("stderr should contain the error output")
	}
}

// ---- SetFormat ------------------------------------------------------------------

func TestSetFormatUnknownDefaultsToJSON(t *testing.T) {
	SetFormat("xml")
	if GetFormat() != "json" {
		t.Errorf("unknown format should default to json, got %q", GetFormat())
	}
}

func TestSetFormatCaseInsensitive(t *testing.T) {
	for _, f := range []string{"JSON", "Json", "TABLE", "YAML"} {
		SetFormat(f)
		got := GetFormat()
		if got != strings.ToLower(f) {
			t.Errorf("SetFormat(%q) → GetFormat() = %q, want %q", f, got, strings.ToLower(f))
		}
	}
}

func TestSetFormatValidFormats(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"json", "json"},
		{"table", "table"},
		{"yaml", "yaml"},
	}
	for _, tc := range tests {
		SetFormat(tc.input)
		if got := GetFormat(); got != tc.want {
			t.Errorf("SetFormat(%q) → %q, want %q", tc.input, got, tc.want)
		}
	}
}
