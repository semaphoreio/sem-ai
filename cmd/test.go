package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/semaphoreio/sem-ai/pkg/testparse"
	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test intelligence — results, failures, flaky detection",
}

var testPipelineFlag string

var testReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Fetch test results for a pipeline by parsing job logs",
	Long: `Analyzes job logs in a pipeline to extract test results.
Supports Go (gotestsum), pytest, rspec, and jest output formats.
Returns structured test data: pass/fail counts, individual failures with file/line.`,
	Example: `  sem-ai test report --pipeline abc123-def456
  sem-ai test report --pipeline abc123-def456 --format table`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		if testPipelineFlag == "" {
			return fmt.Errorf("--pipeline is required")
		}

		reports, err := fetchTestReports(testPipelineFlag)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		output.Result(reports)
		return nil
	},
}

var testSummaryPipelineFlag string

var testSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "AI-friendly test summary for a pipeline",
	Long:  "Compact digest: total/passed/failed/skipped counts, failure details, affected files.",
	Example: `  sem-ai test summary --pipeline abc123-def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		if testSummaryPipelineFlag == "" {
			return fmt.Errorf("--pipeline is required")
		}

		reports, err := fetchTestReports(testSummaryPipelineFlag)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		// Aggregate into summary
		totalTests, totalPassed, totalFailed, totalSkipped := 0, 0, 0, 0
		type failure struct {
			Job     string `json:"job"`
			Test    string `json:"test"`
			File    string `json:"file,omitempty"`
			Line    int    `json:"line,omitempty"`
			Message string `json:"message,omitempty"`
		}
		var failures []failure

		for _, r := range reports {
			if r.Report == nil {
				continue
			}
			totalTests += r.Report.Total
			totalPassed += r.Report.Passed
			totalFailed += r.Report.Failed
			totalSkipped += r.Report.Skipped
			for _, t := range r.Report.Tests {
				if t.Status == "failed" {
					failures = append(failures, failure{
						Job:     r.JobName,
						Test:    t.Name,
						File:    t.File,
						Line:    t.Line,
						Message: t.Message,
					})
				}
			}
		}

		verdict := "passed"
		if totalFailed > 0 {
			verdict = "failed"
		}

		summary := map[string]any{
			"pipeline_id": testSummaryPipelineFlag,
			"verdict":     verdict,
			"total":       totalTests,
			"passed":      totalPassed,
			"failed":      totalFailed,
			"skipped":     totalSkipped,
			"failures":    failures,
		}

		if totalTests == 0 {
			summary["note"] = "no test output detected in job logs — jobs may not produce parseable test output"
		}

		output.Result(summary)
		return nil
	},
}

var (
	testFlakyProjectFlag string
	testFlakyBranchFlag  string
	testFlakyCountFlag   int
)

var testFlakyCmd = &cobra.Command{
	Use:   "flaky",
	Short: "Detect flaky tests by analyzing recent workflows",
	Long: `Analyzes the last N workflows for a project and finds tests that
sometimes pass and sometimes fail — i.e. flaky tests.

Returns each flaky test with its pass/fail ratio across recent runs.

This is a quick per-pipeline snapshot from junit artifacts (works even without
the flaky-tests backend). For history-backed flaky data (weeks of disruptions,
pass-rate, labels) use 'sem-ai flaky list'.`,
	Example: `  sem-ai test flaky --project my-project
  sem-ai test flaky --project my-project --branch main --count 10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(testFlakyProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()

		// Fetch recent workflows
		params := url.Values{}
		params.Set("project_id", projectID)
		if testFlakyBranchFlag != "" {
			params.Set("branch_name", testFlakyBranchFlag)
		}

		resp, err := c.ListWithParams("plumber-workflows", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d", resp.StatusCode), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var workflows []struct {
			WfID         string `json:"wf_id"`
			InitialPplID string `json:"initial_ppl_id"`
			BranchName   string `json:"branch_name"`
		}
		if err := json.Unmarshal(resp.Body, &workflows); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		limit := min(testFlakyCountFlag, len(workflows))

		// Track test outcomes across workflows
		type testKey struct{ name, pkg string }
		outcomes := make(map[testKey][]string) // "passed" or "failed" per workflow

		for _, wf := range workflows[:limit] {
			reports, err := fetchTestReports(wf.InitialPplID)
			if err != nil {
				continue
			}
			for _, r := range reports {
				if r.Report == nil {
					continue
				}
				for _, t := range r.Report.Tests {
					k := testKey{name: t.Name, pkg: t.Package}
					outcomes[k] = append(outcomes[k], t.Status)
				}
			}
		}

		// Find flaky tests (seen both passed and failed)
		type flakyTest struct {
			Name       string `json:"name"`
			Package    string `json:"package,omitempty"`
			PassCount  int    `json:"pass_count"`
			FailCount  int    `json:"fail_count"`
			Appearances int   `json:"appearances"`
			FlakyRate  string `json:"flaky_rate"`
		}

		var flaky []flakyTest
		for k, results := range outcomes {
			passes, fails := 0, 0
			for _, r := range results {
				switch r {
				case "passed":
					passes++
				case "failed":
					fails++
				}
			}
			if passes > 0 && fails > 0 {
				rate := float64(fails) / float64(passes+fails) * 100
				flaky = append(flaky, flakyTest{
					Name:        k.name,
					Package:     k.pkg,
					PassCount:   passes,
					FailCount:   fails,
					Appearances: passes + fails,
					FlakyRate:   fmt.Sprintf("%.0f%%", rate),
				})
			}
		}

		output.Result(map[string]any{
			"project":            testFlakyProjectFlag,
			"branch":             testFlakyBranchFlag,
			"workflows_analyzed": limit,
			"flaky_tests":        flaky,
			"flaky_count":        len(flaky),
		})
		return nil
	},
}

// jobReport pairs a job with its parsed test report.
type jobReport struct {
	JobID   string              `json:"job_id"`
	JobName string              `json:"job_name"`
	Status  string              `json:"job_status"`
	Result  string              `json:"job_result"`
	Report  *testparse.TestReport `json:"test_report"`
}

// fetchTestReports gets pipeline blocks/jobs, tries artifact-based test results
// first (JUnit JSON pushed by test-results CLI), falls back to log parsing.
func fetchTestReports(pipelineID string) ([]jobReport, error) {
	c := client.New()

	// Get pipeline with blocks and jobs
	params := url.Values{}
	params.Set("detailed", "true")
	resp, err := c.ListWithParams("pipelines/"+pipelineID, params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}

	var pplData struct {
		Blocks []struct {
			Name string `json:"name"`
			Jobs []struct {
				Name   string `json:"name"`
				JobID  string `json:"job_id"`
				Status string `json:"status"`
				Result string `json:"result"`
			} `json:"jobs"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(resp.Body, &pplData); err != nil {
		return nil, err
	}

	var reports []jobReport

	for _, block := range pplData.Blocks {
		for _, job := range block.Jobs {
			jr := jobReport{
				JobID:   job.JobID,
				JobName: job.Name,
				Status:  job.Status,
				Result:  job.Result,
			}

			// Strategy 1: Try fetching test-results artifact (pushed by `test-results publish`)
			if report := fetchArtifactTestResults(c, job.JobID); report != nil {
				report.Source = "artifact"
				jr.Report = report
				reports = append(reports, jr)
				continue
			}

			// Strategy 2: Parse test output from job logs
			if report := fetchLogTestResults(c, job.JobID); report != nil {
				report.Source = "log"
				jr.Report = report
			}

			reports = append(reports, jr)
		}
	}

	return reports, nil
}

// fetchArtifactTestResults tries to get JUnit JSON from test-results artifacts.
func fetchArtifactTestResults(c *client.Client, jobID string) *testparse.TestReport {
	// test-results CLI pushes to: test-results/junit.json
	p := url.Values{}
	p.Set("scope", "jobs")
	p.Set("scope_id", jobID)
	p.Set("path", "test-results/junit.json")
	p.Set("method", "GET")

	resp, err := c.ListWithParams("artifacts/signed_url", p)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}

	var signedResp struct {
		Items []struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &signedResp); err != nil || len(signedResp.Items) == 0 {
		return nil
	}

	// Download the actual artifact (may be gzipped) — no auth header for signed URLs
	artifactResp, err := c.GetExternal(signedResp.Items[0].URL)
	if err != nil || artifactResp.StatusCode != 200 {
		return nil
	}

	data := artifactResp.Body

	// Decompress if gzipped (artifacts are typically gzip-compressed)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		decompressed, err := decompressGzip(data)
		if err == nil {
			data = decompressed
		}
	}

	return testparse.ParseJUnitJSON(data)
}

// fetchLogTestResults parses test output from job logs.
func fetchLogTestResults(c *client.Client, jobID string) *testparse.TestReport {
	logResp, err := c.Get("logs", jobID)
	if err != nil || logResp.StatusCode != 200 {
		return nil
	}

	var logs struct {
		Events []struct {
			Output string `json:"output"`
		} `json:"events"`
	}
	if err := json.Unmarshal(logResp.Body, &logs); err != nil {
		return nil
	}

	var sb strings.Builder
	for _, e := range logs.Events {
		if e.Output != "" {
			sb.WriteString(e.Output)
		}
	}

	return testparse.ParseFromLogs(sb.String())
}

func decompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func init() {
	testReportCmd.Flags().StringVar(&testPipelineFlag, "pipeline", "", "pipeline ID (required)")
	testSummaryCmd.Flags().StringVar(&testSummaryPipelineFlag, "pipeline", "", "pipeline ID (required)")
	testFlakyCmd.Flags().StringVar(&testFlakyProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	testFlakyCmd.Flags().StringVar(&testFlakyBranchFlag, "branch", "", "filter by branch")
	testFlakyCmd.Flags().IntVar(&testFlakyCountFlag, "count", 5, "number of recent workflows to analyze")

	testCmd.AddCommand(testReportCmd)
	testCmd.AddCommand(testSummaryCmd)
	testCmd.AddCommand(testFlakyCmd)
	rootCmd.AddCommand(testCmd)
}
