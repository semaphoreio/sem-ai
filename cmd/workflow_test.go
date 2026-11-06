package cmd

import (
	"encoding/json"
	"fmt"
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
// workflows: the first-run case the old reschedule-latest code could not do.
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
	if !strings.Contains(out.String(), "wf-uuid-1") {
		t.Errorf("output missing workflow_id; got %q", out.String())
	}
}

// TestWorkflowRunPropagatesBadRequest is the negative path: a 400 from the
// create endpoint (e.g. an unknown reference) must surface as an error, not be
// silently swallowed.
func TestWorkflowRunPropagatesBadRequest(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/proj-uuid-123":
			writeJSON(w, 200, map[string]any{
				"metadata": map[string]any{"id": "proj-uuid-123"},
			})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/plumber-workflows":
			writeJSON(w, 400, map[string]any{"message": "bad reference"})
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
		t.Fatal("expected error on 400, got nil")
	}
	if !strings.Contains(errb.String(), "400") {
		t.Errorf("stderr should mention 400; got %q", errb.String())
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
	reqs, _, _ := apiMock(t, checkoutProjectHandler(t, 200))
	resetWorkflowRunFlags()
	// --project omitted: detected from the checkout, so the branch would come
	// from git and the detached-HEAD guard is what must fire.
	initCheckout(t, "main")
	runGitIn(t, ".", "checkout", "--detach")

	err := workflowRunCmd.RunE(workflowRunCmd, nil)
	if err == nil {
		t.Fatal("expected error when HEAD is detached and no --branch given")
	}
	if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 0 {
		t.Errorf("must not POST create with reference=HEAD; got %d POSTs", n)
	}
}

// initCheckout makes a temp git repo on branch, whose origin points at the
// repo checkoutProjectHandler reports as project "widget", and chdirs into it.
func initCheckout(t *testing.T, branch string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	runGitIn(t, dir, "init", "-b", branch)
	runGitIn(t, dir, "config", "user.email", "test@example.com")
	runGitIn(t, dir, "config", "user.name", "test")
	runGitIn(t, dir, "commit", "--allow-empty", "-m", "init")
	runGitIn(t, dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
}

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// checkoutProjectHandler serves two projects: "widget" (the checkout's repo,
// proj-uuid-123) and "other" (proj-uuid-999). POST create answers createStatus.
func checkoutProjectHandler(t *testing.T, createStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects":
			writeJSON(w, 200, []map[string]any{
				{
					"metadata": map[string]any{"name": "widget", "id": "proj-uuid-123"},
					"spec":     map[string]any{"repository": map[string]any{"url": "git@github.com:acme/widget.git", "name": "widget"}},
				},
				{
					"metadata": map[string]any{"name": "other", "id": "proj-uuid-999"},
					"spec":     map[string]any{"repository": map[string]any{"url": "git@github.com:acme/other.git", "name": "other"}},
				},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/widget":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-uuid-123"}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects/other":
			writeJSON(w, 200, map[string]any{"metadata": map[string]any{"id": "proj-uuid-999"}})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/plumber-workflows":
			if createStatus == 200 {
				writeJSON(w, 200, map[string]any{"workflow_id": "wf-uuid-1", "pipeline_id": "ppl-uuid-1"})
				return
			}
			writeJSON(w, createStatus, map[string]any{"message": "upstream error"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			writeJSON(w, 404, map[string]any{"message": "not found"})
		}
	}
}

// TestWorkflowRunCheckoutDefaults: with no flags in a checkout, the project
// and branch come from git, and no commit_sha is sent (the server runs the
// pushed branch head, never a possibly-unpushed local HEAD).
func TestWorkflowRunCheckoutDefaults(t *testing.T) {
	reqs, _, _ := apiMock(t, checkoutProjectHandler(t, 200))
	resetWorkflowRunFlags()
	initCheckout(t, "feat")

	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err != nil {
		t.Fatalf("workflow run failed: %v", err)
	}
	create := find(t, reqs, "POST", "/api/v1alpha/plumber-workflows")
	if create.Body["project_id"] != "proj-uuid-123" || create.Body["reference"] != "feat" {
		t.Errorf("body = %v, want project proj-uuid-123 on feat", create.Body)
	}
	if _, ok := create.Body["commit_sha"]; ok {
		t.Errorf("commit_sha must not default from local HEAD; body = %v", create.Body)
	}
}

// TestWorkflowRunExplicitBranchNoLocalCommit: --branch release from a checkout
// on feat sends reference release and no commit_sha (feat's HEAD is not on
// release).
func TestWorkflowRunExplicitBranchNoLocalCommit(t *testing.T) {
	reqs, _, _ := apiMock(t, checkoutProjectHandler(t, 200))
	resetWorkflowRunFlags()
	initCheckout(t, "feat")
	wfRunBranchFlag = "release"
	wfRunPipelineFlag = ".semaphore/deploy.yml"

	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err != nil {
		t.Fatalf("workflow run failed: %v", err)
	}
	create := find(t, reqs, "POST", "/api/v1alpha/plumber-workflows")
	if create.Body["reference"] != "release" {
		t.Errorf("reference = %v, want release", create.Body["reference"])
	}
	if _, ok := create.Body["commit_sha"]; ok {
		t.Errorf("commit_sha must not be sent without --commit; body = %v", create.Body)
	}
	if create.Body["pipeline_file"] != ".semaphore/deploy.yml" {
		t.Errorf("pipeline_file = %v, want .semaphore/deploy.yml", create.Body["pipeline_file"])
	}
}

// TestWorkflowRunExplicitProjectMatchingCheckout: --project naming this
// checkout's project still defaults the branch from git (the documented
// `workflow run --project <p>` usage inside the repo keeps working).
func TestWorkflowRunExplicitProjectMatchingCheckout(t *testing.T) {
	reqs, _, _ := apiMock(t, checkoutProjectHandler(t, 200))
	resetWorkflowRunFlags()
	initCheckout(t, "feat")
	wfRunProjectFlag = "widget"

	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err != nil {
		t.Fatalf("workflow run failed: %v", err)
	}
	create := find(t, reqs, "POST", "/api/v1alpha/plumber-workflows")
	if create.Body["project_id"] != "proj-uuid-123" || create.Body["reference"] != "feat" {
		t.Errorf("body = %v, want project proj-uuid-123 on feat", create.Body)
	}
}

// TestWorkflowRunOtherProjectRequiresBranch: --project naming a different
// project must not borrow this checkout's branch; it errors with zero POSTs.
func TestWorkflowRunOtherProjectRequiresBranch(t *testing.T) {
	reqs, _, errb := apiMock(t, checkoutProjectHandler(t, 200))
	resetWorkflowRunFlags()
	initCheckout(t, "feat")
	wfRunProjectFlag = "other"

	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err == nil {
		t.Fatal("expected an error: --project other without --branch")
	}
	if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 0 {
		t.Errorf("must not POST for another project without --branch; got %d", n)
	}
	if !strings.Contains(errb.String(), "pass --branch") {
		t.Errorf("stderr should ask for --branch; got %q", errb.String())
	}

	// With --branch it goes through, for the other project, with no commit.
	wfRunBranchFlag = "main"
	if err := workflowRunCmd.RunE(workflowRunCmd, nil); err != nil {
		t.Fatalf("workflow run with --branch failed: %v", err)
	}
	create := find(t, reqs, "POST", "/api/v1alpha/plumber-workflows")
	if create.Body["project_id"] != "proj-uuid-999" || create.Body["reference"] != "main" {
		t.Errorf("body = %v, want project proj-uuid-999 on main", create.Body)
	}
	if _, ok := create.Body["commit_sha"]; ok {
		t.Errorf("commit_sha must not be sent; body = %v", create.Body)
	}
}

// TestWorkflowRunNoRetryOnServerError: the create endpoint is not idempotent,
// so a 5xx or 429 must be sent exactly once (a retry could create a duplicate
// workflow), surface as an error, and point the user at `workflow list`.
func TestWorkflowRunNoRetryOnServerError(t *testing.T) {
	for _, status := range []int{500, 503, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			reqs, out, errb := apiMock(t, checkoutProjectHandler(t, status))
			resetWorkflowRunFlags()
			wfRunProjectFlag = "widget"
			wfRunBranchFlag = "main"

			if err := workflowRunCmd.RunE(workflowRunCmd, nil); err == nil {
				t.Fatalf("expected an error on HTTP %d", status)
			}
			if n := count(reqs, "POST", "/api/v1alpha/plumber-workflows"); n != 1 {
				t.Errorf("POST create sent %d times, want exactly 1 (no retry)", n)
			}
			if !strings.Contains(errb.String(), fmt.Sprint(status)) {
				t.Errorf("stderr should mention %d; got %q", status, errb.String())
			}
			if status >= 500 && !strings.Contains(errb.String(), "workflow list") {
				t.Errorf("stderr should point at `workflow list`; got %q", errb.String())
			}
			if strings.Contains(out.String(), "workflow_id") {
				t.Errorf("must not report success; stdout=%q", out.String())
			}
		})
	}
}
