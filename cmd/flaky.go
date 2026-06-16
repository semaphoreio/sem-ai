package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/semaphoreio/sem-ai/pkg/testparse"
	"github.com/spf13/cobra"
)

var flakyCmd = &cobra.Command{
	Use:   "flaky",
	Short: "History-backed flaky-test signals (Superjerry) for a project",
	Long: `Query a project's flaky-test history from the Test Results API.

This is the server-backed history view (weeks of disruptions, pass-rate, labels).
For a quick single-pipeline snapshot from junit artifacts, see 'sem-ai test flaky'.`,
}

type flakyFilters struct {
	Branch, CommitSha, TestName, Group, File, Suite, Runner string
	Label, Resolved, Scheduled, Age, PassRate, Disruptions  string
	DateFrom, DateTo                                        string
}

func (f flakyFilters) toValues() url.Values {
	v := url.Values{}
	add := func(key, val string) {
		if val != "" {
			v.Set(key, val)
		}
	}
	add("branch", f.Branch)
	add("commit_sha", f.CommitSha)
	add("test_name", f.TestName)
	add("group", f.Group)
	add("file", f.File)
	add("suite", f.Suite)
	add("runner", f.Runner)
	add("label", f.Label)
	add("resolved", f.Resolved)
	add("scheduled", f.Scheduled)
	add("age", f.Age)
	add("pass_rate", f.PassRate)
	add("disruptions", f.Disruptions)
	add("date_from", f.DateFrom)
	add("date_to", f.DateTo)
	return v
}

func flakyListParams(f flakyFilters, page, pageSize int, sortField, sortDir string) url.Values {
	v := f.toValues()
	v.Set("page", fmt.Sprintf("%d", page))
	v.Set("page_size", fmt.Sprintf("%d", clampPageSize(pageSize)))
	if sortField != "" {
		v.Set("sort_field", sortField)
	}
	if sortDir != "" {
		v.Set("sort_dir", sortDir)
	}
	return v
}

func pagedFilterParams(f flakyFilters, page, pageSize int) url.Values {
	v := f.toValues()
	v.Set("page", fmt.Sprintf("%d", page))
	v.Set("page_size", fmt.Sprintf("%d", clampPageSize(pageSize)))
	return v
}

// clampPageSize bounds a requested page size to [1, 100] (the server also caps at 100).
func clampPageSize(n int) int {
	switch {
	case n > 100:
		return 100
	case n < 1:
		return 1
	default:
		return n
	}
}

func addFilterFlags(cmd *cobra.Command, f *flakyFilters) {
	fl := cmd.Flags()
	fl.StringVar(&f.Branch, "branch", "", "filter by git branch (wildcards allowed)")
	fl.StringVar(&f.CommitSha, "commit-sha", "", "filter by commit SHA")
	fl.StringVar(&f.TestName, "test-name", "", "filter by test name")
	fl.StringVar(&f.Group, "group", "", "filter by test group")
	fl.StringVar(&f.File, "file", "", "filter by test file")
	fl.StringVar(&f.Suite, "suite", "", "filter by test suite")
	fl.StringVar(&f.Runner, "runner", "", "filter by test runner/framework")
	fl.StringVar(&f.Label, "label", "", "filter by label(s), comma-separated")
	fl.StringVar(&f.Resolved, "resolved", "", "filter by resolved status (true|false)")
	fl.StringVar(&f.Scheduled, "scheduled", "", "filter by scheduled status (true|false)")
	fl.StringVar(&f.Age, "age", "", "filter by age in days, with operator e.g. >=30")
	fl.StringVar(&f.PassRate, "pass-rate", "", "filter by pass rate %, with operator e.g. <50")
	fl.StringVar(&f.Disruptions, "disruptions", "", "filter by disruption count, with operator e.g. >5")
	fl.StringVar(&f.DateFrom, "date-from", "", "start date YYYY-MM-DD or now-Nd")
	fl.StringVar(&f.DateTo, "date-to", "", "end date YYYY-MM-DD or now+Nd")
}

func flakyResourcePath(projectID, testID, sub string) string {
	p := fmt.Sprintf("projects/%s/test_results/flaky_tests/%s", projectID, testID)
	if sub != "" {
		p += "/" + sub
	}
	return p
}

func flakyTrendsPath(projectID, metric string) (string, error) {
	switch metric {
	case "", "flaky":
		return fmt.Sprintf("projects/%s/test_results/flaky_history", projectID), nil
	case "disruptions":
		return fmt.Sprintf("projects/%s/test_results/disruption_history", projectID), nil
	default:
		return "", fmt.Errorf("invalid --metric %q (want flaky|disruptions)", metric)
	}
}

// emitJSON decodes a 200 body and prints it; otherwise emits a structured error.
func emitJSON(resp *client.Response) error { return emitJSONTransform(resp, nil) }

// emitJSONTransform decodes a 200 body, applies an optional transform, prints it.
func emitJSONTransform(resp *client.Response, transform func(any) any) error {
	if resp.StatusCode != 200 {
		output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
		return fmt.Errorf("API returned %d", resp.StatusCode)
	}
	var result any
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		output.Error("parse_error", fmt.Sprintf("failed to decode response: %v", err), 1)
		return err
	}
	if transform != nil {
		result = transform(result)
	}
	output.Result(result)
	return nil
}

// stripDisruptionHistory removes the heavy per-test disruption_history field
// from each record of a flaky-list array, keeping `flaky list` output compact.
// The full history is available via `flaky show <test_id>`. Non-array inputs
// (e.g. an error object) pass through unchanged.
func stripDisruptionHistory(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			delete(m, "disruption_history")
		}
	}
	return v
}

// ---- flaky list ----

var (
	flakyListProject  string
	flakyListPage     int
	flakyListPageSize int
	flakyListSortF    string
	flakyListSortD    string
	flakyListFilters  flakyFilters
	flakyListFull     bool
)

var flakyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List flaky tests for a project",
	Example: `  sem-ai flaky list --project my-project
  sem-ai flaky list --project my-project --branch main --resolved false
  sem-ai flaky list --project my-project --pass-rate "<80" --sort-field pass_rate --sort-dir asc`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(flakyListProject)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		params := flakyListParams(flakyListFilters, flakyListPage, flakyListPageSize, flakyListSortF, flakyListSortD)

		c := client.New()
		resp, err := c.ListWithParams(fmt.Sprintf("projects/%s/test_results/flaky_tests", projectID), params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if flakyListFull {
			return emitJSON(resp)
		}
		return emitJSONTransform(resp, stripDisruptionHistory)
	},
}

// ---- flaky show ----

var (
	flakyShowProject string
	flakyShowFilters flakyFilters
)

var flakyShowCmd = &cobra.Command{
	Use:     "show <test_id>",
	Short:   "Show details for a single flaky test (per-context pass rate, p95, disruptions)",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai flaky show 3f2a... --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(flakyShowProject)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		resp, err := c.ListWithParams(flakyResourcePath(projectID, args[0], ""), flakyShowFilters.toValues())
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

// ---- flaky disruptions ----

var (
	flakyDisrProject  string
	flakyDisrPage     int
	flakyDisrPageSize int
	flakyDisrFilters  flakyFilters
)

var flakyDisruptionsCmd = &cobra.Command{
	Use:     "disruptions <test_id>",
	Short:   "List individual disruption occurrences for a flaky test",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai flaky disruptions 3f2a... --project my-project --page-size 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(flakyDisrProject)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		resp, err := c.ListWithParams(
			flakyResourcePath(projectID, args[0], "disruptions"),
			pagedFilterParams(flakyDisrFilters, flakyDisrPage, flakyDisrPageSize),
		)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

// ---- flaky trends ----

var (
	flakyTrendsProject string
	flakyTrendsMetric  string
	flakyTrendsFilters flakyFilters
)

var flakyTrendsCmd = &cobra.Command{
	Use:   "trends",
	Short: "Project-level flaky/disruption count time series (day, count)",
	Example: `  sem-ai flaky trends --project my-project
  sem-ai flaky trends --project my-project --metric disruptions --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(flakyTrendsProject)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		path, err := flakyTrendsPath(projectID, flakyTrendsMetric)
		if err != nil {
			output.Error("usage_error", err.Error(), 1)
			return err
		}
		c := client.New()
		resp, err := c.ListWithParams(path, flakyTrendsFilters.toValues())
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

// ---- flaky failure ----

var (
	flakyFailureProject string
	flakyFailureRunID   string
	flakyFailurePage    int
	flakyFailureFilters flakyFilters
)

// flakyFailureResult is the structured output of the `flaky failure` command.
type flakyFailureResult struct {
	TestID    string                  `json:"test_id"`
	TestName  string                  `json:"test_name"`
	RunID     string                  `json:"run_id"`
	Framework string                  `json:"framework"`
	Summary   flakyFailureSummary     `json:"summary"`
	Matched   bool                    `json:"matched"`
	Failures  []flakyFailureTestEntry `json:"failures"`
}

type flakyFailureSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type flakyFailureTestEntry struct {
	Name    string `json:"name"`
	Package string `json:"package,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message,omitempty"`
}

var flakyFailureCmd = &cobra.Command{
	Use:   "failure <test_id>",
	Short: "Show the real failure for a flaky test by fetching its disruption job log",
	Long: `Resolves the latest disruption's job id, fetches that job's log, and
extracts the failing test's assertion/message from the log output.

Use --run-id to skip disruption resolution and fetch a specific job directly.`,
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai flaky failure 3f2a... --project my-project
  sem-ai flaky failure 3f2a... --project my-project --run-id <job-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		testID := args[0]

		projectID, err := resolveProjectID(flakyFailureProject)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()

		// Step 1: resolve job id
		jobID := flakyFailureRunID
		if jobID == "" {
			jobID, err = resolveDisruptionJobID(c, projectID, testID)
			if err != nil {
				return err
			}
		}

		// Step 2: get the test name for filtering
		testName := resolveTestName(c, projectID, testID, flakyFailureFilters)

		// Step 3: fetch the job log
		rawLog, err := fetchJobLog(c, jobID)
		if err != nil {
			return err
		}

		// Step 4: parse
		report := testparse.ParseFromLogs(rawLog)
		if report == nil {
			output.Error("unparsed", "could not parse test output from job log (unknown framework)", 1)
			return fmt.Errorf("unparsed log")
		}

		// Step 5: filter to the target test and build result
		result := buildFailureResult(testID, testName, jobID, report)
		output.Result(result)
		return nil
	},
}

// resolveDisruptionJobID fetches the disruptions list and returns the first non-empty run_id.
func resolveDisruptionJobID(c *client.Client, projectID, testID string) (string, error) {
	resp, err := c.ListWithParams(
		flakyResourcePath(projectID, testID, "disruptions"),
		pagedFilterParams(flakyFilters{}, 1, 10),
	)
	if err != nil {
		output.Error("api_error", err.Error(), 1)
		return "", err
	}
	if resp.StatusCode != 200 {
		output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
		return "", fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var disruptions []struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(resp.Body, &disruptions); err != nil {
		output.Error("parse_error", fmt.Sprintf("failed to decode disruptions: %v", err), 1)
		return "", err
	}

	for _, d := range disruptions {
		if d.RunID != "" {
			return d.RunID, nil
		}
	}

	output.Error("no_disruptions", "no disruptions with a run_id found for this test", 1)
	return "", fmt.Errorf("no disruptions")
}

// resolveTestName fetches the flaky test record and returns its name field.
// Returns empty string on any error — the caller treats it as best-effort.
func resolveTestName(c *client.Client, projectID, testID string, filters flakyFilters) string {
	resp, err := c.ListWithParams(flakyResourcePath(projectID, testID, ""), filters.toValues())
	if err != nil {
		return ""
	}
	if resp.StatusCode != 200 {
		return ""
	}
	var record struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(resp.Body, &record); err != nil {
		return ""
	}
	return record.Name
}

// fetchJobLog fetches a job's log and returns the concatenated cmd_output text.
func fetchJobLog(c *client.Client, jobID string) (string, error) {
	resp, err := c.Get("logs", jobID)
	if err != nil {
		output.Error("api_error", err.Error(), 1)
		return "", err
	}
	if resp.StatusCode != 200 {
		msg := fmt.Sprintf("job log for %s unavailable (HTTP %d) — likely past retention; diagnose from source", jobID, resp.StatusCode)
		output.Error("log_unavailable", msg, resp.StatusCode)
		return "", fmt.Errorf("log unavailable: HTTP %d", resp.StatusCode)
	}

	var logs struct {
		Events []struct {
			Type   string `json:"event"`
			Output string `json:"output"`
		} `json:"events"`
	}
	if err := json.Unmarshal(resp.Body, &logs); err != nil {
		output.Error("parse_error", fmt.Sprintf("failed to decode job log: %v", err), 1)
		return "", err
	}

	var sb strings.Builder
	for _, e := range logs.Events {
		if e.Type == "cmd_output" {
			sb.WriteString(e.Output)
		}
	}
	return sb.String(), nil
}

// buildFailureResult matches failures from the report against the test name
// and assembles the command's output struct.
func buildFailureResult(testID, testName, jobID string, report *testparse.TestReport) flakyFailureResult {
	result := flakyFailureResult{
		TestID:   testID,
		TestName: testName,
		RunID:    jobID,
		Framework: report.Framework,
		Summary: flakyFailureSummary{
			Total:  report.Total,
			Passed: report.Passed,
			Failed: report.Failed,
		},
	}

	// Normalise the flaky test name for matching: strip leading "test " / "doctest "
	needle := strings.TrimSpace(testName)
	needle = strings.TrimPrefix(needle, "doctest ")
	needle = strings.TrimPrefix(needle, "test ")

	var matched []flakyFailureTestEntry
	for _, t := range report.Tests {
		if t.Status != "failed" {
			continue
		}
		if needle != "" && (strings.Contains(t.Name, needle) || strings.Contains(needle, t.Name)) {
			matched = append(matched, toEntry(t))
		}
	}

	if len(matched) > 0 {
		result.Matched = true
		result.Failures = matched
	} else {
		result.Matched = false
		// Return all failures so the caller still has something to work with
		for _, t := range report.Tests {
			if t.Status == "failed" {
				result.Failures = append(result.Failures, toEntry(t))
			}
		}
	}

	return result
}

func toEntry(t testparse.TestResult) flakyFailureTestEntry {
	return flakyFailureTestEntry{
		Name:    t.Name,
		Package: t.Package,
		File:    t.File,
		Line:    t.Line,
		Message: t.Message,
	}
}

func init() {
	flakyListCmd.Flags().StringVar(&flakyListProject, "project", "", "project name or ID (required)")
	flakyListCmd.Flags().IntVar(&flakyListPage, "page", 1, "page number")
	flakyListCmd.Flags().IntVar(&flakyListPageSize, "page-size", 20, "results per page")
	flakyListCmd.Flags().StringVar(&flakyListSortF, "sort-field", "", "sort field (e.g. total_disruptions_count, pass_rate)")
	flakyListCmd.Flags().StringVar(&flakyListSortD, "sort-dir", "", "sort direction (asc|desc)")
	flakyListCmd.Flags().BoolVar(&flakyListFull, "full", false, "include full disruption_history per test (default: omit for compact output)")
	addFilterFlags(flakyListCmd, &flakyListFilters)

	flakyShowCmd.Flags().StringVar(&flakyShowProject, "project", "", "project name or ID (required)")
	addFilterFlags(flakyShowCmd, &flakyShowFilters)

	flakyDisruptionsCmd.Flags().StringVar(&flakyDisrProject, "project", "", "project name or ID (required)")
	flakyDisruptionsCmd.Flags().IntVar(&flakyDisrPage, "page", 1, "page number")
	flakyDisruptionsCmd.Flags().IntVar(&flakyDisrPageSize, "page-size", 10, "results per page")
	addFilterFlags(flakyDisruptionsCmd, &flakyDisrFilters)

	flakyTrendsCmd.Flags().StringVar(&flakyTrendsProject, "project", "", "project name or ID (required)")
	flakyTrendsCmd.Flags().StringVar(&flakyTrendsMetric, "metric", "flaky", "series: flaky|disruptions")
	addFilterFlags(flakyTrendsCmd, &flakyTrendsFilters)

	flakyFailureCmd.Flags().StringVar(&flakyFailureProject, "project", "", "project name or ID (required)")
	flakyFailureCmd.Flags().StringVar(&flakyFailureRunID, "run-id", "", "use this job id directly instead of resolving the latest disruption")
	flakyFailureCmd.Flags().IntVar(&flakyFailurePage, "page", 1, "page number for disruption lookup")
	addFilterFlags(flakyFailureCmd, &flakyFailureFilters)

	flakyCmd.AddCommand(flakyListCmd)
	flakyCmd.AddCommand(flakyShowCmd)
	flakyCmd.AddCommand(flakyDisruptionsCmd)
	flakyCmd.AddCommand(flakyTrendsCmd)
	flakyCmd.AddCommand(flakyFailureCmd)
	rootCmd.AddCommand(flakyCmd)
}
