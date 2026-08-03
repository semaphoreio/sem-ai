package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/testparse"
)

// ---- test fixtures/mock server ---------------------------------------------------
//
// fetchTestReports feeds `test report`, `test summary`, and `test flaky`. These
// tests exercise it directly against a fake API that can independently make
// each of the three sources (workflow-scope unified report, job-scope
// artifact, job log) present, absent (404), or malformed, to prove the
// fallback chain picks the right one and never errors out on a bad source.

type fakeJob struct {
	name, id, status, result string
}

// artifactKey identifies a fake artifact by the same scope/scope_id/path
// triple the real artifacts/signed_url endpoint is queried with.
func artifactKey(scope, scopeID, path string) string {
	return scope + "|" + scopeID + "|" + path
}

type testAPIConfig struct {
	pipelineID string
	wfID       string
	jobs       []fakeJob
	// artifacts maps an artifactKey to the raw bytes served for it. Absence
	// of a key simulates a 404 from artifacts/signed_url.
	artifacts map[string][]byte
	// logs maps a job ID to its raw (concatenated) log output. Absence of a
	// key simulates a 404 from the logs endpoint.
	logs map[string]string
}

// gzipBytes compresses data the same way test-results artifacts typically
// are, so tests can prove the fetch path transparently gunzips.
func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// newTestAPI starts a fake Semaphore API serving pipeline detail, the
// artifacts/signed_url + blob download pair, and job logs, wired into the
// client via the base-URL test seam. Any request outside cfg's fixtures gets
// a 404, mirroring a real missing artifact/job.
func newTestAPI(t *testing.T, cfg testAPIConfig) *httptest.Server {
	t.Helper()

	var srv *httptest.Server
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1alpha/pipelines/"+cfg.pipelineID, func(w http.ResponseWriter, r *http.Request) {
		type jobJSON struct {
			Name   string `json:"name"`
			JobID  string `json:"job_id"`
			Status string `json:"status"`
			Result string `json:"result"`
		}
		jobs := make([]jobJSON, 0, len(cfg.jobs))
		for _, j := range cfg.jobs {
			jobs = append(jobs, jobJSON{Name: j.name, JobID: j.id, Status: j.status, Result: j.result})
		}
		resp := map[string]any{
			"pipeline": map[string]any{"wf_id": cfg.wfID},
			"blocks": []map[string]any{
				{"name": "block-1", "jobs": jobs},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1alpha/artifacts/signed_url", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		key := artifactKey(q.Get("scope"), q.Get("scope_id"), q.Get("path"))
		if _, ok := cfg.artifacts[key]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"items": []map[string]any{
				{"path": q.Get("path"), "url": srv.URL + "/blob/" + url.QueryEscape(key)},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		key, err := url.QueryUnescape(strings.TrimPrefix(r.URL.Path, "/blob/"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data, ok := cfg.artifacts[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/api/v1alpha/logs/", func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimPrefix(r.URL.Path, "/api/v1alpha/logs/")
		logOutput, ok := cfg.logs[jobID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"events": []map[string]any{{"output": logOutput}},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client.SetBaseURLForTest(srv.URL)
	t.Cleanup(func() { client.SetBaseURLForTest("") })

	t.Setenv("SEMAPHORE_API_TOKEN", "test-token")
	t.Setenv("SEMAPHORE_HOST", "example.test")
	config.Load()

	return srv
}

// semaphoreJSON builds a Semaphore test-results JSON payload (the schema
// both job-scope junit.json and the workflow-scope unified report share):
// {"testResults": [{summary, suites: [{tests: [...]}]}]}. Each test entry is
// {name, state, jobID (optional, for workflow-scope job attribution),
// message (optional, on failure)}.
type semaphoreTestEntry struct {
	name    string
	state   string
	jobID   string
	message string
}

func semaphoreJSON(t *testing.T, framework string, entries []semaphoreTestEntry) []byte {
	t.Helper()

	total, passed, failed, skipped := 0, 0, 0, 0
	type testJSON struct {
		Name    string         `json:"name"`
		State   string         `json:"state"`
		Failure map[string]any `json:"failure,omitempty"`
		SemEnv  map[string]any `json:"semaphoreEnv,omitempty"`
	}
	tests := make([]testJSON, 0, len(entries))
	for _, e := range entries {
		total++
		switch e.state {
		case "passed":
			passed++
		case "failed", "error":
			failed++
		case "skipped":
			skipped++
		}
		tj := testJSON{Name: e.name, State: e.state}
		if e.message != "" {
			tj.Failure = map[string]any{"message": e.message}
		}
		if e.jobID != "" {
			tj.SemEnv = map[string]any{"jobId": e.jobID}
		}
		tests = append(tests, tj)
	}

	payload := map[string]any{
		"testResults": []map[string]any{
			{
				"name":      "suite",
				"framework": framework,
				"summary": map[string]any{
					"total": total, "passed": passed, "failed": failed, "skipped": skipped, "error": 0,
				},
				"suites": []map[string]any{
					{"name": "MySuite", "tests": tests},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal semaphore JSON: %v", err)
	}
	return data
}

func junitXML(t *testing.T, entries []semaphoreTestEntry) []byte {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`<testsuite name="suite">`)
	for _, e := range entries {
		sb.WriteString(`<testcase name="` + e.name + `" classname="pkg">`)
		switch e.state {
		case "failed":
			sb.WriteString(`<failure message="` + e.message + `"/>`)
		case "error":
			sb.WriteString(`<error message="` + e.message + `"/>`)
		case "skipped":
			sb.WriteString(`<skipped/>`)
		}
		sb.WriteString(`</testcase>`)
	}
	sb.WriteString(`</testsuite>`)
	return []byte(sb.String())
}

// ---- fetchTestReports: fallback chain ---------------------------------------------

func TestFetchTestReportsFallbackChain(t *testing.T) {
	const pipelineID = "ppl-1"
	const wfID = "wf-1"
	job1 := fakeJob{name: "unit", id: "job-1", status: "done", result: "failed"}
	job2 := fakeJob{name: "integration", id: "job-2", status: "done", result: "passed"}

	t.Run("workflow report present -> used, per job by job_id", func(t *testing.T) {
		wfData := semaphoreJSON(t, "go", []semaphoreTestEntry{
			{name: "TestUnitA", state: "passed", jobID: "job-1"},
			{name: "TestUnitB", state: "failed", jobID: "job-1", message: "boom"},
			{name: "TestIntegrationA", state: "passed", jobID: "job-2"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1, job2},
			artifacts: map[string][]byte{
				artifactKey("workflows", wfID, "test-results/"+pipelineID+".json"): wfData,
				// Deliberately no job-scope artifacts and no logs: if the
				// chain reaches for either, the test fails below.
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		if len(reports) != 2 {
			t.Fatalf("expected 2 job reports, got %d", len(reports))
		}

		byJob := map[string]jobReport{}
		for _, r := range reports {
			byJob[r.JobID] = r
		}

		r1 := byJob["job-1"]
		if r1.Report == nil {
			t.Fatal("job-1: expected a report")
		}
		if r1.Report.Source != "workflow-artifact" {
			t.Errorf("job-1 Source = %q, want workflow-artifact", r1.Report.Source)
		}
		if r1.Report.Total != 2 || r1.Report.Passed != 1 || r1.Report.Failed != 1 {
			t.Errorf("job-1 counts = %+v, want total=2 passed=1 failed=1", r1.Report)
		}

		r2 := byJob["job-2"]
		if r2.Report == nil || r2.Report.Source != "workflow-artifact" {
			t.Fatalf("job-2: expected workflow-artifact report, got %+v", r2.Report)
		}
		if r2.Report.Total != 1 || r2.Report.Passed != 1 {
			t.Errorf("job-2 counts = %+v, want total=1 passed=1", r2.Report)
		}
	})

	t.Run("workflow absent -> falls back to job-scope junit.json", func(t *testing.T) {
		jobData := semaphoreJSON(t, "pytest", []semaphoreTestEntry{
			{name: "test_foo", state: "failed", message: "AssertionError"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts: map[string][]byte{
				// no workflow-scope key at all -> 404
				artifactKey("jobs", "job-1", "test-results/junit.json"): jobData,
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		if len(reports) != 1 {
			t.Fatalf("expected 1 job report, got %d", len(reports))
		}
		r := reports[0]
		if r.Report == nil || r.Report.Source != "artifact" {
			t.Fatalf("expected artifact-sourced report, got %+v", r.Report)
		}
		if r.Report.Framework != "pytest" || r.Report.Failed != 1 {
			t.Errorf("got %+v, want framework=pytest failed=1", r.Report)
		}
	})

	t.Run("workflow + job json absent -> falls back to job-scope junit.xml", func(t *testing.T) {
		xmlData := junitXML(t, []semaphoreTestEntry{
			{name: "TestFromXML", state: "failed", message: "xml failure"},
			{name: "TestPassedXML", state: "passed"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts: map[string][]byte{
				artifactKey("jobs", "job-1", "test-results/junit.xml"): xmlData,
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		r := reports[0]
		if r.Report == nil || r.Report.Source != "artifact" {
			t.Fatalf("expected artifact-sourced (XML) report, got %+v", r.Report)
		}
		if r.Report.Framework != "junit" || r.Report.Total != 2 || r.Report.Failed != 1 || r.Report.Passed != 1 {
			t.Errorf("got %+v, want framework=junit total=2 failed=1 passed=1", r.Report)
		}
	})

	t.Run("all artifact sources absent -> falls back to log parsing", func(t *testing.T) {
		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts:  map[string][]byte{}, // nothing published anywhere
			logs: map[string]string{
				"job-1": "=== RUN   TestFoo\n--- FAIL: . TestFoo (0.01s)\nDONE 3 tests, 1 failures in 0.5s",
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		r := reports[0]
		if r.Report == nil || r.Report.Source != "log" {
			t.Fatalf("expected log-sourced report, got %+v", r.Report)
		}
		if r.Report.Framework != "go" || r.Report.Total != 3 || r.Report.Failed != 1 {
			t.Errorf("got %+v, want framework=go total=3 failed=1", r.Report)
		}
	})

	t.Run("malformed workflow report -> clean fallback to job scope, no error", func(t *testing.T) {
		jobData := semaphoreJSON(t, "go", []semaphoreTestEntry{
			{name: "TestOK", state: "passed"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts: map[string][]byte{
				artifactKey("workflows", wfID, "test-results/"+pipelineID+".json"): []byte("{not valid json"),
				artifactKey("jobs", "job-1", "test-results/junit.json"):            jobData,
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports should not error on a malformed artifact: %v", err)
		}
		r := reports[0]
		if r.Report == nil || r.Report.Source != "artifact" {
			t.Fatalf("expected clean fallback to job-scope artifact, got %+v", r.Report)
		}
	})

	t.Run("malformed job-scope xml, no logs -> clean empty result, no error", func(t *testing.T) {
		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts: map[string][]byte{
				artifactKey("jobs", "job-1", "test-results/junit.xml"): []byte("<not-xml-at-all"),
			},
			// no logs either
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports should not error when every source is malformed/absent: %v", err)
		}
		if len(reports) != 1 {
			t.Fatalf("expected 1 job report (metadata still returned), got %d", len(reports))
		}
		if reports[0].Report != nil {
			t.Errorf("expected nil Report when nothing parses, got %+v", reports[0].Report)
		}
		if reports[0].JobID != "job-1" {
			t.Errorf("job metadata should still be populated: %+v", reports[0])
		}
	})

	t.Run("workflow report present but job not attributed -> falls back for that job", func(t *testing.T) {
		// The unified report only carries job-1's tests (e.g. an older
		// publish without job-id tagging for job-2). job-2 must fall
		// through to its own job-scope artifact rather than get an empty
		// workflow-sourced report.
		wfData := semaphoreJSON(t, "go", []semaphoreTestEntry{
			{name: "TestUnitA", state: "passed", jobID: "job-1"},
		})
		job2Data := semaphoreJSON(t, "rspec", []semaphoreTestEntry{
			{name: "spec_a", state: "passed"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1, job2},
			artifacts: map[string][]byte{
				artifactKey("workflows", wfID, "test-results/"+pipelineID+".json"): wfData,
				artifactKey("jobs", "job-2", "test-results/junit.json"):            job2Data,
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		byJob := map[string]jobReport{}
		for _, r := range reports {
			byJob[r.JobID] = r
		}

		if byJob["job-1"].Report == nil || byJob["job-1"].Report.Source != "workflow-artifact" {
			t.Errorf("job-1 should be workflow-artifact sourced, got %+v", byJob["job-1"].Report)
		}
		if byJob["job-2"].Report == nil || byJob["job-2"].Report.Source != "artifact" {
			t.Errorf("job-2 should fall back to its own job-scope artifact, got %+v", byJob["job-2"].Report)
		}
	})

	t.Run("workflow-scope payload gzip compressed -> transparently decompressed", func(t *testing.T) {
		wfData := semaphoreJSON(t, "go", []semaphoreTestEntry{
			{name: "TestGz", state: "passed", jobID: "job-1"},
		})

		cfg := testAPIConfig{
			pipelineID: pipelineID,
			wfID:       wfID,
			jobs:       []fakeJob{job1},
			artifacts: map[string][]byte{
				artifactKey("workflows", wfID, "test-results/"+pipelineID+".json"): gzipBytes(t, wfData),
			},
		}
		newTestAPI(t, cfg)

		reports, err := fetchTestReports(pipelineID)
		if err != nil {
			t.Fatalf("fetchTestReports: %v", err)
		}
		if reports[0].Report == nil || reports[0].Report.Source != "workflow-artifact" {
			t.Fatalf("expected gunzipped workflow-artifact report, got %+v", reports[0].Report)
		}
		if reports[0].Report.Total != 1 || reports[0].Report.Passed != 1 {
			t.Errorf("got %+v, want total=1 passed=1", reports[0].Report)
		}
	})
}

// ---- sliceReportForJob ------------------------------------------------------------

func TestSliceReportForJob(t *testing.T) {
	report := &testparse.TestReport{
		Framework: "go",
		Tests: []testparse.TestResult{
			{Name: "A", Status: "passed", JobID: "job-1"},
			{Name: "B", Status: "failed", JobID: "job-1"},
			{Name: "C", Status: "skipped", JobID: "job-1"},
			{Name: "D", Status: "passed", JobID: "job-2"},
		},
	}

	t.Run("matches only the requested job", func(t *testing.T) {
		got := sliceReportForJob(report, "job-1")
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		if got.Total != 3 || got.Passed != 1 || got.Failed != 1 || got.Skipped != 1 {
			t.Errorf("got %+v, want total=3 passed=1 failed=1 skipped=1", got)
		}
		if got.Framework != "go" {
			t.Errorf("Framework = %q, want go", got.Framework)
		}
	})

	t.Run("no match returns nil", func(t *testing.T) {
		if got := sliceReportForJob(report, "job-does-not-exist"); got != nil {
			t.Errorf("expected nil for unmatched job, got %+v", got)
		}
	})

	t.Run("nil report returns nil", func(t *testing.T) {
		if got := sliceReportForJob(nil, "job-1"); got != nil {
			t.Errorf("expected nil for nil report, got %+v", got)
		}
	})

	t.Run("empty job id returns nil", func(t *testing.T) {
		if got := sliceReportForJob(report, ""); got != nil {
			t.Errorf("expected nil for empty job id, got %+v", got)
		}
	})
}
