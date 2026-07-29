package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDebugDisabledMsg(t *testing.T) {
	msg := debugDisabledMsg("my-app", nil)

	if !strings.Contains(msg, "my-app") {
		t.Errorf("message should name the project, got: %q", msg)
	}
	if !strings.Contains(msg, "Collaborators can start empty debug sessions") {
		t.Errorf("message should point at the exact setting to enable, got: %q", msg)
	}
	if strings.Contains(msg, "Server response") {
		t.Errorf("message should omit the server-response line when body is empty, got: %q", msg)
	}
}

func TestDebugDisabledMsgWithBody(t *testing.T) {
	body := []byte(`{"code":7,"message":"You are not allowed to debug this project"}`)
	msg := debugDisabledMsg("my-app", body)

	if !strings.Contains(msg, "Server response") {
		t.Errorf("message should include the server response when body is present, got: %q", msg)
	}
	if !strings.Contains(msg, "not allowed to debug this project") {
		t.Errorf("message should surface the raw server body, got: %q", msg)
	}
}

// ---- jobAge ----

func TestJobAge_ParsesUnixSecondsString(t *testing.T) {
	fiveMinAgo := time.Now().Add(-5 * time.Minute).Unix()
	age := jobAge(fmt.Sprintf("%d", fiveMinAgo))
	if age == "" {
		t.Fatal("expected a non-empty age")
	}
	d, err := time.ParseDuration(age)
	if err != nil {
		t.Fatalf("age %q is not a parseable duration: %v", age, err)
	}
	if d < 4*time.Minute || d > 6*time.Minute {
		t.Errorf("age = %v, want ~5m", d)
	}
}

func TestJobAge_EmptyOrUnparseable(t *testing.T) {
	for _, in := range []string{"", "not-a-number", "0", "-5"} {
		if got := jobAge(in); got != "" {
			t.Errorf("jobAge(%q) = %q, want empty", in, got)
		}
	}
}

// ---- testbox list ----

func testboxJobFixture(id, name, projectID, machine string, ageAgo time.Duration) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"id":          id,
			"name":        name,
			"create_time": fmt.Sprintf("%d", time.Now().Add(-ageAgo).Unix()),
		},
		"spec": map[string]any{
			"project_id": projectID,
			"agent": map[string]any{
				"machine": map[string]any{"type": machine},
			},
		},
		"status": map[string]any{"state": "RUNNING"},
	}
}

func TestTestboxList_ReturnsTestboxesAndExcludesOtherJobs(t *testing.T) {
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/jobs":
			if got := r.URL.Query().Get("states"); got != "RUNNING" {
				t.Errorf("states query = %q, want RUNNING", got)
			}
			writeJSON(w, 200, map[string]any{
				"jobs": []map[string]any{
					testboxJobFixture("box-1", testboxJobName, "proj-1", "f1-standard-2", 5*time.Minute),
					testboxJobFixture("other-1", "some other debug job", "proj-1", "f1-standard-4", time.Minute),
				},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/projects":
			writeJSON(w, 200, []map[string]any{
				{"metadata": map[string]any{"id": "proj-1", "name": "go-mux"}},
			})
		default:
			writeJSON(w, 500, map[string]any{"path": r.URL.Path})
		}
	})

	if err := testboxListCmd.RunE(testboxListCmd, nil); err != nil {
		t.Fatalf("testbox list: %v", err)
	}
	find(t, reqs, "GET", "/api/v1alpha/jobs")

	var result struct {
		Testboxes []map[string]any `json:"testboxes"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	if len(result.Testboxes) != 1 {
		t.Fatalf("expected exactly 1 testbox (non-testbox job excluded), got %d: %+v", len(result.Testboxes), result.Testboxes)
	}
	box := result.Testboxes[0]
	if box["testbox_id"] != "box-1" {
		t.Errorf("testbox_id = %v, want box-1", box["testbox_id"])
	}
	if box["machine"] != "f1-standard-2" {
		t.Errorf("machine = %v, want f1-standard-2", box["machine"])
	}
	if box["project"] != "go-mux" {
		t.Errorf("project = %v, want go-mux (resolved name, not raw id)", box["project"])
	}
	age, _ := box["age"].(string)
	if age == "" {
		t.Error("expected a non-empty age")
	}
}

func TestTestboxList_EmptyWhenNoneRunning(t *testing.T) {
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1alpha/jobs":
			writeJSON(w, 200, map[string]any{"jobs": []map[string]any{}, "next_page_token": ""})
		default:
			writeJSON(w, 500, nil)
		}
	})

	if err := testboxListCmd.RunE(testboxListCmd, nil); err != nil {
		t.Fatalf("testbox list should not error on empty result: %v", err)
	}

	var result struct {
		Testboxes []map[string]any `json:"testboxes"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	if result.Testboxes == nil {
		t.Error("testboxes should be an empty array, not null/absent")
	}
	if len(result.Testboxes) != 0 {
		t.Errorf("expected 0 testboxes, got %d", len(result.Testboxes))
	}
}

func TestTestboxList_NoRunningJobsAtAllIsNotAnError(t *testing.T) {
	_, _, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"jobs": []map[string]any{}})
	})
	if err := testboxListCmd.RunE(testboxListCmd, nil); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if errb.String() != "" {
		t.Errorf("expected no stderr output, got: %q", errb.String())
	}
}

// ---- testbox stop ----

func resetTestboxStopFlags() {
	testboxStopID = ""
	testboxStopTimeoutFlag = 2 * time.Second
	testboxStopPollInterval = 5 * time.Millisecond
}

func TestTestboxStop_MissingID_Errors(t *testing.T) {
	apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 500, nil) // should never be hit
	})
	resetTestboxStopFlags()
	if err := testboxStopCmd.RunE(testboxStopCmd, nil); err == nil {
		t.Fatal("expected error when --id is not set")
	}
}

func TestTestboxStop_IssuesJobStopAndConfirmsFinished(t *testing.T) {
	stopCalled := false
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/jobs/box-1/stop":
			stopCalled = true
			writeJSON(w, 200, map[string]any{"job_id": "box-1", "status": "stopping"})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/jobs/box-1":
			// First GET (pre-check) sees it still running; every GET after
			// the stop call (the confirmation poll) sees it finished.
			if !stopCalled {
				writeJSON(w, 200, map[string]any{"status": map[string]any{"state": "RUNNING", "result": "NONE"}})
			} else {
				writeJSON(w, 200, map[string]any{"status": map[string]any{"state": "FINISHED", "result": "STOPPED"}})
			}
		default:
			writeJSON(w, 500, map[string]any{"path": r.URL.Path})
		}
	})

	resetTestboxStopFlags()
	testboxStopID = "box-1"

	if err := testboxStopCmd.RunE(testboxStopCmd, nil); err != nil {
		t.Fatalf("testbox stop: %v", err)
	}

	// The real fix under test: stop must actually hit the job-stop endpoint,
	// the same one `job stop` uses.
	find(t, reqs, "POST", "/api/v1alpha/jobs/box-1/stop")

	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	if result["status"] != "stopped" {
		t.Errorf("status = %q, want stopped (job confirmed FINISHED)", result["status"])
	}
	if result["state"] != "FINISHED" {
		t.Errorf("state = %q, want FINISHED", result["state"])
	}
}

func TestTestboxStop_AlreadyFinished_SkipsStopCall(t *testing.T) {
	reqs, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/jobs/box-2":
			writeJSON(w, 200, map[string]any{"status": map[string]any{"state": "FINISHED", "result": "STOPPED"}})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/jobs/box-2/stop":
			t.Error("should not issue a stop call for an already-finished job")
			writeJSON(w, 200, map[string]any{})
		default:
			writeJSON(w, 500, nil)
		}
	})

	resetTestboxStopFlags()
	testboxStopID = "box-2"

	if err := testboxStopCmd.RunE(testboxStopCmd, nil); err != nil {
		t.Fatalf("testbox stop: %v", err)
	}
	if n := count(reqs, "POST", "/api/v1alpha/jobs/box-2/stop"); n != 0 {
		t.Errorf("expected no stop call, got %d", n)
	}

	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	if result["status"] != "already_stopped" {
		t.Errorf("status = %q, want already_stopped", result["status"])
	}
}

func TestTestboxStop_JobGone404_HandledGracefully(t *testing.T) {
	reqs, out, errb := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/jobs/box-3":
			writeJSON(w, 404, map[string]any{"message": "not found"})
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/jobs/box-3/stop":
			writeJSON(w, 404, map[string]any{"message": "not found"})
		default:
			writeJSON(w, 500, nil)
		}
	})

	resetTestboxStopFlags()
	testboxStopID = "box-3"

	if err := testboxStopCmd.RunE(testboxStopCmd, nil); err != nil {
		t.Fatalf("expected a missing testbox to be handled gracefully, not errored: %v", err)
	}
	find(t, reqs, "POST", "/api/v1alpha/jobs/box-3/stop")
	if errb.String() != "" {
		t.Errorf("expected no error output for an absent testbox, got: %q", errb.String())
	}

	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	if result["status"] != "not_found" {
		t.Errorf("status = %q, want not_found", result["status"])
	}
}

func TestTestboxStop_NeverConfirms_ReportsStopRequestedNotFalseStopped(t *testing.T) {
	_, out, _ := apiMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1alpha/jobs/box-4/stop":
			writeJSON(w, 200, map[string]any{"status": "stopping"})
		case r.Method == "GET" && r.URL.Path == "/api/v1alpha/jobs/box-4":
			// Stays RUNNING forever — server never confirms within our window.
			writeJSON(w, 200, map[string]any{"status": map[string]any{"state": "RUNNING", "result": "NONE"}})
		default:
			writeJSON(w, 500, nil)
		}
	})

	resetTestboxStopFlags()
	testboxStopID = "box-4"
	testboxStopTimeoutFlag = 20 * time.Millisecond
	testboxStopPollInterval = 5 * time.Millisecond

	if err := testboxStopCmd.RunE(testboxStopCmd, nil); err != nil {
		t.Fatalf("testbox stop: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("could not parse output %q: %v", out.String(), err)
	}
	// Must NOT claim "stopped" when the job never actually confirmed it.
	if result["status"] == "stopped" {
		t.Errorf("status = %q, must not falsely report stopped when job stayed RUNNING", result["status"])
	}
	if result["status"] != "stop_requested" {
		t.Errorf("status = %q, want stop_requested", result["status"])
	}
}
