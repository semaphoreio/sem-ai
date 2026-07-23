package cmd

import (
	"encoding/json"
	"testing"
)

func TestSummarizeWorkflows(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{
			"wf_id": "wf-1",
			"branch_name": "main",
			"commit_sha": "abc123",
			"initial_ppl_id": "ppl-1",
			"created_at": {"seconds": 1784800746, "nanos": 5},
			"project_id": "proj-1",
			"hook_id": "h-1",
			"requester_id": "u-1"
		}`),
		json.RawMessage(`broken`),
	}

	got := summarizeWorkflows(raw)
	if len(got) != 1 {
		t.Fatalf("summarizeWorkflows returned %d items, want 1", len(got))
	}
	w := got[0]
	if w.ID != "wf-1" || w.Branch != "main" || w.PipelineID != "ppl-1" {
		t.Errorf("unexpected summary: %+v", w)
	}
	if w.CreatedAt != "2026-07-23T09:59:06Z" {
		t.Errorf("created_at: got %q", w.CreatedAt)
	}

	out, _ := json.Marshal(got)
	for _, dropped := range []string{"project_id", "hook_id", "requester_id"} {
		if containsSubstring(string(out), dropped) {
			t.Errorf("summary output still contains %q", dropped)
		}
	}
}
