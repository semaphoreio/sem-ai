package cmd

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
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

// resetWorkflowRunFlags zeroes the package-level flags workflow run reads, so a
// value set by one test never leaks into the next (RunE is invoked directly,
// bypassing cobra's per-run flag reset).
func resetWorkflowRunFlags() {
	wfRunProjectFlag, wfRunBranchFlag, wfRunCommitFlag, wfRunPipelineFlag = "", "", "", ""
}

// TestWorkflowRunCreatesWorkflow is the positive path: `workflow run` must POST
// to plumber-workflows to CREATE a new workflow (not GET+reschedule), passing
// project_id + reference, and succeed on a project that has zero prior
// workflows — the first-run case the old reschedule-latest code could not do.
func TestWorkflowRunCreatesWorkflow(t *testing.T) {
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/my-project":
			writeJSON(w, 200, map[string]any{
				"metadata": map[string]any{"id": "proj-uuid-123"},
			})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/plumber-workflows":
			writeJSON(w, 200, map[string]any{
				"workflow_id": "wf-uuid-1",
				"pipeline_id": "ppl-uuid-1",
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			writeJSON(w, 404, map[string]any{"message": "not found"})
		}
	})
	resetWorkflowRunFlags()
	wfRunProjectFlag = "my-project"
	wfRunBranchFlag = "feature-x"
	wfRunCommitFlag = "deadbeef"

	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err != nil {
		t.Fatalf("workflow run failed: %v", err)
	}

	// Must have created via POST, and never fallen back to reschedule.
	if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 1 {
		t.Fatalf("expected 1 POST create, got %d; reqs=%+v", n, *reqs)
	}
	for _, rq := range *reqs {
		if strings.Contains(rq.Path, "/reschedule") {
			t.Fatalf("workflow run must create, not reschedule; saw %s %s", rq.Method, rq.Path)
		}
	}

	create := find(t, reqs, "POST", "/api/v1alpha/plumber-workflows")
	if create.Body["project_id"] != "proj-uuid-123" {
		t.Errorf("project_id = %v, want proj-uuid-123", create.Body["project_id"])
	}
	if create.Body["reference"] != "feature-x" {
		t.Errorf("reference = %v, want feature-x", create.Body["reference"])
	}
	if create.Body["commit_sha"] != "deadbeef" {
		t.Errorf("commit_sha = %v, want deadbeef", create.Body["commit_sha"])
	}
	if rt, ok := create.Body["request_token"].(string); !ok || rt == "" {
		t.Errorf("request_token missing or empty in create payload; got %v", create.Body["request_token"])
	}
	if !strings.Contains(out.String(), "wf-uuid-1") {
		t.Errorf("output missing workflow_id; got %q", out.String())
	}
}

// TestWorkflowRunPropagatesForbidden is the negative path: a 403 from the
// create endpoint (caller lacks project.job.rerun) must surface as an error,
// not be silently swallowed.
func TestWorkflowRunPropagatesForbidden(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-uuid-123":
			writeJSON(w, 200, map[string]any{
				"metadata": map[string]any{"id": "proj-uuid-123"},
			})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/plumber-workflows":
			writeJSON(w, 403, map[string]any{"message": "Not Found"})
		default:
			writeJSON(w, 404, map[string]any{"message": "not found"})
		}
	})
	resetWorkflowRunFlags()
	wfRunProjectFlag = "proj-uuid-123"
	wfRunBranchFlag = "main"
	wfRunCommitFlag = "abc123"

	err := workflowRunCmd.RunE(workflowRunCmd, nil)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(errb.String(), "403") {
		t.Errorf("stderr should mention 403; got %q", errb.String())
	}
}

// TestWorkflowRunPropagatesNotFound mirrors the real server: the public API
// masks an RBAC denial on POST /workflows as HTTP 404 "Not Found" (see
// public-api authorize.ex), so the realistic permission-denied path is a 404,
// not a 403. It must surface as an error, never a silent success.
func TestWorkflowRunPropagatesNotFound(t *testing.T) {
	_, out, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-uuid-123":
			writeJSON(w, 200, map[string]any{
				"metadata": map[string]any{"id": "proj-uuid-123"},
			})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/plumber-workflows":
			writeJSON(w, 404, map[string]any{"message": "Not Found"})
		default:
			writeJSON(w, 404, map[string]any{"message": "not found"})
		}
	})
	resetWorkflowRunFlags()
	wfRunProjectFlag = "proj-uuid-123"
	wfRunBranchFlag = "main"
	wfRunCommitFlag = "abc123"

	err := workflowRunCmd.RunE(workflowRunCmd, nil)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(errb.String(), "404") {
		t.Errorf("stderr should mention 404; got %q", errb.String())
	}
	if strings.Contains(out.String(), "workflow_id") {
		t.Errorf("must not report success on a denial; stdout=%q", out.String())
	}
}

// TestWorkflowRunRequiresBranch: when no --branch is given and none can be
// detected from git, the command must error clearly rather than POST a workflow
// with an empty reference.
func TestWorkflowRunRequiresBranch(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-uuid-123" {
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-uuid-123"}})
			return
		}
		writeJSON(w, 404, map[string]any{"message": "not found"})
	})
	resetWorkflowRunFlags()
	wfRunProjectFlag = "proj-uuid-123"
	// no branch flag; force git detection to fail via a non-repo CWD
	dir := t.TempDir()
	t.Chdir(dir)

	err := workflowRunCmd.RunE(workflowRunCmd, nil)
	if err == nil {
		t.Fatal("expected error when no branch and not in a git repo")
	}
	if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 0 {
		t.Errorf("must not POST create without a reference; got %d POSTs", n)
	}
}

// TestWorkflowRunRequiresBranchDetachedHEAD: in a detached HEAD,
// `git rev-parse --abbrev-ref HEAD` prints the literal string "HEAD" (not
// empty, not an error). Without the guard, that string would be posted as
// the workflow's reference, which the server cannot schedule against. It
// must be treated the same as "no branch detected".
func TestWorkflowRunRequiresBranchDetachedHEAD(t *testing.T) {
	reqs, _, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-uuid-123" {
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-uuid-123"}})
			return
		}
		writeJSON(w, 404, map[string]any{"message": "not found"})
	})
	resetWorkflowRunFlags()
	wfRunProjectFlag = "proj-uuid-123"

	dir := t.TempDir()
	t.Chdir(dir)
	runGit := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")
	runGit("commit", "--allow-empty", "-m", "init")
	runGit("checkout", "--detach")

	err := workflowRunCmd.RunE(workflowRunCmd, nil)
	if err == nil {
		t.Fatal("expected error when HEAD is detached and no --branch given")
	}
	if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 0 {
		t.Errorf("must not POST create with reference=HEAD; got %d POSTs", n)
	}
}
