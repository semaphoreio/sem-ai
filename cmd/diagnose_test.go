package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// resetDiagnoseFlags zeroes the diagnose command's package-level flags so a
// value set by one test never bleeds into the next (RunE is invoked
// directly, bypassing cobra's per-run flag reset).
func resetDiagnoseFlags() {
	diagnoseProjectFlag, diagnoseBranchFlag = "", ""
}

// pipelineWithJob builds a minimal detailed-pipeline response with a single
// failed job in a single block.
func pipelineWithJob(jobID string) map[string]any {
	return map[string]any{
		"pipeline": map[string]any{
			"ppl_id": "ppl-1",
			"name":   "Build",
			"state":  "done",
			"result": "failed",
		},
		"blocks": []map[string]any{
			{
				"name":   "Test",
				"result": "failed",
				"jobs": []map[string]any{
					{"name": "unit-tests", "job_id": jobID, "status": "finished", "result": "FAILED"},
				},
			},
		},
	}
}

// diagnosedJob pulls the single failed_jobs entry out of a diagnose result.
func diagnosedJob(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var result struct {
		FailedJobs []map[string]any `json:"failed_jobs"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("failed to parse diagnose output: %v\n%s", err, out)
	}
	if len(result.FailedJobs) != 1 {
		t.Fatalf("expected 1 failed job, got %d: %+v", len(result.FailedJobs), result.FailedJobs)
	}
	return result.FailedJobs[0]
}

// ---- direction 1: jobs that DID run and have logs still show them ----

func TestDiagnose_FailedJob_WithLogs_ShowsLogTail(t *testing.T) {
	resetDiagnoseFlags()
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/plumber-workflows/wf-1":
			writeJSON(w, 200, map[string]any{"workflow": map[string]any{"initial_ppl_id": "ppl-1"}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/pipelines/ppl-1":
			writeJSON(w, 200, pipelineWithJob("job-1"))
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/logs/job-1":
			writeJSON(w, 200, map[string]any{
				"events": []map[string]any{
					{"event": "cmd_started", "directive": "make test"},
					{"event": "cmd_output", "output": "FAIL: TestFoo\n"},
					{"event": "cmd_finished", "directive": "make test", "exit_code": 1},
				},
			})
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})

	if err := diagnoseCmd.RunE(diagnoseCmd, []string{"wf-1"}); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	job := diagnosedJob(t, out.Bytes())
	if job["failure_reason"] != nil {
		t.Errorf("failure_reason = %v, want absent for a job with logs", job["failure_reason"])
	}
	logTail, _ := job["log_tail"].(string)
	if logTail == "" || !strings.Contains(logTail, "FAIL: TestFoo") {
		t.Errorf("log_tail = %q, want it to contain the captured output", logTail)
	}
}

// ---- direction 2: jobs that never ran now surface failure_reason ----

func TestDiagnose_FailedJob_NeverRan_ShowsFailureReasonFrom404Body(t *testing.T) {
	resetDiagnoseFlags()
	reason := "Selected machine type is not available in this organization"
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/plumber-workflows/wf-1":
			writeJSON(w, 200, map[string]any{"workflow": map[string]any{"initial_ppl_id": "ppl-1"}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/pipelines/ppl-1":
			writeJSON(w, 200, pipelineWithJob("job-2"))
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/logs/job-2":
			// Backend semaphore#1091: 404 with a plain JSON-encoded string body
			// carrying the job's failure_reason.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			b, _ := json.Marshal(reason)
			_, _ = w.Write(b)
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})

	if err := diagnoseCmd.RunE(diagnoseCmd, []string{"wf-1"}); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	job := diagnosedJob(t, out.Bytes())
	if job["failure_reason"] != reason {
		t.Errorf("failure_reason = %v, want %q", job["failure_reason"], reason)
	}
	if job["log_tail"] != nil {
		t.Errorf("log_tail = %v, want absent for a job that never ran", job["log_tail"])
	}
}

func TestDiagnose_FailedJob_NeverRan_ShowsNeverStartedMessage(t *testing.T) {
	resetDiagnoseFlags()
	reason := "This job never started, so no logs were produced."
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/plumber-workflows/wf-1":
			writeJSON(w, 200, map[string]any{"workflow": map[string]any{"initial_ppl_id": "ppl-1"}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/pipelines/ppl-1":
			writeJSON(w, 200, pipelineWithJob("job-3"))
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/logs/job-3":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			b, _ := json.Marshal(reason)
			_, _ = w.Write(b)
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})

	if err := diagnoseCmd.RunE(diagnoseCmd, []string{"wf-1"}); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	job := diagnosedJob(t, out.Bytes())
	if job["failure_reason"] != reason {
		t.Errorf("failure_reason = %v, want %q", job["failure_reason"], reason)
	}
}

// ---- direction 3: genuine fetch errors are not mistaken for "job never ran" ----

func TestDiagnose_FailedJob_LogFetchGenuineError_LeavesJobUnexplained(t *testing.T) {
	resetDiagnoseFlags()
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/plumber-workflows/wf-1":
			writeJSON(w, 200, map[string]any{"workflow": map[string]any{"initial_ppl_id": "ppl-1"}})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/pipelines/ppl-1":
			writeJSON(w, 200, pipelineWithJob("job-5"))
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/logs/job-5":
			// A genuine API error distinct from "not found" - e.g. an auth
			// problem. Must not be treated as "job never ran".
			writeJSON(w, 403, map[string]any{"message": "forbidden"})
		default:
			writeJSON(w, 500, map[string]any{"error": "unexpected " + r.Method + " " + r.URL.Path})
		}
	})

	if err := diagnoseCmd.RunE(diagnoseCmd, []string{"wf-1"}); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	job := diagnosedJob(t, out.Bytes())
	if job["failure_reason"] != nil {
		t.Errorf("failure_reason = %v, want absent - a 403 is not a meaningful explanation", job["failure_reason"])
	}
	if job["log_tail"] != nil {
		t.Errorf("log_tail = %v, want absent", job["log_tail"])
	}
	// The job itself should still be reported, just without fabricated detail.
	if job["job_id"] != "job-5" {
		t.Errorf("job_id = %v, want job-5", job["job_id"])
	}
}

// ---- decode404Body: pure-function table test ----

func TestDecode404Body(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "JSON-encoded string (semaphore#1091 shape)",
			body: `"Selected machine type is not available in this organization"`,
			want: "Selected machine type is not available in this organization",
		},
		{
			name: "JSON-encoded string with whitespace",
			body: `"  This job never started, so no logs were produced.  "`,
			want: "This job never started, so no logs were produced.",
		},
		{
			name: "plain text fallback (not JSON)",
			body: "Internal error",
			want: "Internal error",
		},
		{
			name: "empty JSON string",
			body: `""`,
			want: defaultNoLogsReason,
		},
		{
			name: "empty body",
			body: "",
			want: defaultNoLogsReason,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decode404Body([]byte(tc.body))
			if got != tc.want {
				t.Errorf("decode404Body(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}
