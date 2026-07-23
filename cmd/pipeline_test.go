package cmd

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestSummarizePipelines(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{
			"ppl_id": "ppl-1",
			"wf_id": "wf-1",
			"name": "Deploy Sxmoon",
			"state": "DONE",
			"result": "PASSED",
			"result_reason": "TEST",
			"branch_name": "master",
			"commit_sha": "6aae865340ea17ac909b22e20bd3cbf2e90008de",
			"commit_message": "first line of commit\n\nlong body that should be dropped",
			"yaml_file_name": "sxmoon.yml",
			"error_description": "",
			"created_at": {"seconds": 1784800746, "nanos": 274178000},
			"done_at": {"seconds": 1784800946, "nanos": 0},
			"env_vars": [],
			"queue": {"name": "prod", "type": "implicit"},
			"triggerer": {"wf_triggered_by": 0},
			"organization_id": "org-1",
			"branch_id": "b-1",
			"hook_id": "h-1"
		}`),
		json.RawMessage(`{
			"ppl_id": "ppl-2",
			"wf_id": "wf-2",
			"name": "Running one",
			"state": "RUNNING",
			"branch_name": "main",
			"commit_sha": "abc",
			"commit_message": "single line",
			"yaml_file_name": "semaphore.yml",
			"created_at": {"seconds": 1784800770, "nanos": 0},
			"done_at": {"seconds": 0, "nanos": 0}
		}`),
		json.RawMessage(`not-json`),
	}

	got := summarizePipelines(raw)
	if len(got) != 2 {
		t.Fatalf("summarizePipelines returned %d items, want 2 (malformed item skipped)", len(got))
	}

	p := got[0]
	if p.ID != "ppl-1" || p.WorkflowID != "wf-1" {
		t.Errorf("ids: got %q/%q", p.ID, p.WorkflowID)
	}
	if p.Result != "PASSED" || p.ResultReason != "TEST" {
		t.Errorf("result: got %q/%q", p.Result, p.ResultReason)
	}
	if p.CommitMessage != "first line of commit" {
		t.Errorf("commit_message not trimmed to first line: %q", p.CommitMessage)
	}
	if p.CreatedAt != "2026-07-23T09:59:06Z" {
		t.Errorf("created_at: got %q", p.CreatedAt)
	}
	if p.DoneAt == "" {
		t.Errorf("done_at should be set for done pipeline")
	}

	q := got[1]
	if q.Result != "" || q.DoneAt != "" {
		t.Errorf("running pipeline should have empty result/done_at, got %q/%q", q.Result, q.DoneAt)
	}

	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, dropped := range []string{"env_vars", "queue", "triggerer", "organization_id", "branch_id", "hook_id"} {
		if containsSubstring(string(out), dropped) {
			t.Errorf("summary output still contains %q", dropped)
		}
	}
}

func TestSummarizePipelinesSize(t *testing.T) {
	item := json.RawMessage(`{
		"ppl_id": "ppl-1", "wf_id": "wf-1", "name": "Deploy", "state": "DONE",
		"result": "PASSED", "branch_name": "master",
		"commit_sha": "6aae865340ea17ac909b22e20bd3cbf2e90008de",
		"commit_message": "a commit subject line",
		"yaml_file_name": "semaphore.yml",
		"created_at": {"seconds": 1784800746}, "done_at": {"seconds": 1784800946}
	}`)
	items := make([]json.RawMessage, 30)
	for i := range items {
		items[i] = item
	}
	out, err := json.Marshal(summarizePipelines(items))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 25000 {
		t.Errorf("30 summarized pipelines = %d bytes, must stay well under MCP limits", len(out))
	}
}

func TestListWindowParams(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	v := listWindowParams(url.Values{}, 30, 30, now)
	wantAfter := now.AddDate(0, 0, -30).Unix()
	if v.Get("created_after") != jsonInt(wantAfter) {
		t.Errorf("created_after: got %q, want %d", v.Get("created_after"), wantAfter)
	}
	if v.Get("page_size") != "30" {
		t.Errorf("page_size: got %q, want 30", v.Get("page_size"))
	}

	v = listWindowParams(url.Values{}, 0, 0, now)
	if v.Has("created_after") || v.Has("page_size") {
		t.Errorf("days=0/limit=0 must not set filters, got %v", v)
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line", "one line"},
		{"subject\nbody", "subject"},
		{"subject\r\nbody", "subject"},
		{"  padded  \nrest", "padded"},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstLine(c.in); got != c.want {
			t.Errorf("firstLine(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestProtoTimeString(t *testing.T) {
	if got := protoTimeString(0); got != "" {
		t.Errorf("protoTimeString(0)=%q, want empty", got)
	}
	if got := protoTimeString(1784800746); got != "2026-07-23T09:59:06Z" {
		t.Errorf("protoTimeString(1784800746)=%q", got)
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
