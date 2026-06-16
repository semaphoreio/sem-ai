package testparse

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// TestResult represents a parsed test result from job logs.
type TestResult struct {
	Name     string `json:"name"`
	Package  string `json:"package,omitempty"`
	Status   string `json:"status"` // passed, failed, skipped
	Duration string `json:"duration,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message,omitempty"`
}

// TestReport is the parsed summary from a job's test output.
type TestReport struct {
	Framework string       `json:"framework"`
	Source    string       `json:"source,omitempty"` // "artifact" or "log"
	Total     int          `json:"total"`
	Passed    int          `json:"passed"`
	Failed    int          `json:"failed"`
	Skipped   int          `json:"skipped"`
	Duration  string       `json:"duration,omitempty"`
	Tests     []TestResult `json:"tests,omitempty"`
}

// ansiRe strips ANSI escape sequences from terminal output.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Patterns for various test frameworks
var (
	// gotestsum/go test: "DONE 11 tests, 1 failure in 0.553s"
	goSummaryRe = regexp.MustCompile(`DONE (\d+) tests?,\s*(\d+) failures?\s*(?:,\s*(\d+) skipped)?\s*in (.+)`)

	// gotestsum FAIL line: "FAIL: . Test_timeHandler_statusCode (0.00s)"
	goFailRe = regexp.MustCompile(`FAIL:\s+(\S+)\s+(\S+)\s+\(([^)]+)\)`)

	// go test file:line: "    main_test.go:243: expected status 201, got 200"
	goFileLineRe = regexp.MustCompile(`^\s+(\S+\.go):(\d+):\s+(.+)`)

	// pytest: "5 passed, 2 failed, 1 skipped in 1.23s"
	pytestSummaryRe = regexp.MustCompile(`(\d+) passed(?:,\s*(\d+) failed)?(?:,\s*(\d+) skipped)?\s+in\s+(.+)`)

	// pytest FAILED: "FAILED tests/test_foo.py::test_bar - AssertionError"
	pytestFailRe = regexp.MustCompile(`FAILED\s+(\S+)\s*-\s*(.+)`)

	// rspec: "10 examples, 2 failures, 1 pending"
	rspecSummaryRe = regexp.MustCompile(`(\d+) examples?,\s*(\d+) failures?(?:,\s*(\d+) pending)?`)

	// jest: "Tests: 2 failed, 8 passed, 10 total"
	jestSummaryRe = regexp.MustCompile(`Tests:\s+(\d+) failed,\s*(\d+) passed,\s*(\d+) total`)

	// ExUnit (Elixir): "240 tests, 0 failures" or "1 doctest, 240 tests, 0 failures"
	exunitSummaryRe = regexp.MustCompile(`(?:(\d+) doctests?,\s*)?(\d+) tests?,\s*(\d+) failures?(?:,\s*(\d+) excluded)?`)

	// ExUnit timing: "Finished in 11.0 seconds"
	exunitTimingRe = regexp.MustCompile(`Finished in ([0-9.]+) seconds`)

	// ExUnit failure header: "  1) test foo bar (Some.Module.Test)"
	exunitHeaderRe = regexp.MustCompile(`^\s+\d+\)\s+(?:test|doctest)\s+(.+?)\s+\(([\w.]+)\)\s*$`)

	// ExUnit location line immediately after header: "     test/foo_test.exs:42"
	exunitLocationRe = regexp.MustCompile(`^\s+(\S+):(\d+)\s*$`)

	// minitest (Ruby): "10 runs, 20 assertions, 1 failures, 0 errors, 0 skips"
	minitestSummaryRe = regexp.MustCompile(`(\d+) runs?,\s*\d+ assertions?,\s*(\d+) failures?,\s*(\d+) errors?,\s*(\d+) skips?`)
)

// ParseFromLogs extracts test results from raw job log output.
func ParseFromLogs(logOutput string) *TestReport {
	logOutput = stripANSI(logOutput)
	// Try each framework parser
	if r := parseGoTest(logOutput); r != nil {
		return r
	}
	if r := parsePytest(logOutput); r != nil {
		return r
	}
	if r := parseRspec(logOutput); r != nil {
		return r
	}
	if r := parseJest(logOutput); r != nil {
		return r
	}
	if r := parseExUnit(logOutput); r != nil {
		return r
	}
	if r := parseMinitest(logOutput); r != nil {
		return r
	}
	return nil
}

func parseGoTest(output string) *TestReport {
	m := goSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	total, _ := strconv.Atoi(m[1])
	failed, _ := strconv.Atoi(m[2])
	skipped := 0
	if m[3] != "" {
		skipped, _ = strconv.Atoi(m[3])
	}

	report := &TestReport{
		Framework: "go",
		Total:     total,
		Passed:    total - failed - skipped,
		Failed:    failed,
		Skipped:   skipped,
		Duration:  m[4],
	}

	// Extract individual failures
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		fm := goFailRe.FindStringSubmatch(line)
		if fm == nil {
			continue
		}
		tr := TestResult{
			Package:  fm[1],
			Name:     fm[2],
			Status:   "failed",
			Duration: fm[3],
		}

		// Look ahead for file:line and message
		for j := i + 1; j < len(lines) && j < i+10; j++ {
			flm := goFileLineRe.FindStringSubmatch(lines[j])
			if flm != nil {
				tr.File = flm[1]
				tr.Line, _ = strconv.Atoi(flm[2])
				tr.Message = flm[3]
				break
			}
		}

		report.Tests = append(report.Tests, tr)
	}

	return report
}

func parsePytest(output string) *TestReport {
	m := pytestSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	passed, _ := strconv.Atoi(m[1])
	failed := 0
	if m[2] != "" {
		failed, _ = strconv.Atoi(m[2])
	}
	skipped := 0
	if m[3] != "" {
		skipped, _ = strconv.Atoi(m[3])
	}

	report := &TestReport{
		Framework: "pytest",
		Total:     passed + failed + skipped,
		Passed:    passed,
		Failed:    failed,
		Skipped:   skipped,
		Duration:  m[4],
	}

	// Extract failures
	for _, fm := range pytestFailRe.FindAllStringSubmatch(output, -1) {
		report.Tests = append(report.Tests, TestResult{
			Name:    fm[1],
			Status:  "failed",
			Message: fm[2],
		})
	}

	return report
}

func parseRspec(output string) *TestReport {
	m := rspecSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	total, _ := strconv.Atoi(m[1])
	failed, _ := strconv.Atoi(m[2])
	skipped := 0
	if m[3] != "" {
		skipped, _ = strconv.Atoi(m[3])
	}

	return &TestReport{
		Framework: "rspec",
		Total:     total,
		Passed:    total - failed - skipped,
		Failed:    failed,
		Skipped:   skipped,
	}
}

func parseJest(output string) *TestReport {
	m := jestSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	failed, _ := strconv.Atoi(m[1])
	passed, _ := strconv.Atoi(m[2])
	total, _ := strconv.Atoi(m[3])

	return &TestReport{
		Framework: "jest",
		Total:     total,
		Passed:    passed,
		Failed:    failed,
		Skipped:   total - passed - failed,
	}
}

func parseExUnit(output string) *TestReport {
	m := exunitSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	doctests := 0
	if m[1] != "" {
		doctests, _ = strconv.Atoi(m[1])
	}
	tests, _ := strconv.Atoi(m[2])
	failed, _ := strconv.Atoi(m[3])
	excluded := 0
	if m[4] != "" {
		excluded, _ = strconv.Atoi(m[4])
	}

	total := tests + doctests
	report := &TestReport{
		Framework: "exunit",
		Total:     total,
		Passed:    total - failed - excluded,
		Failed:    failed,
		Skipped:   excluded,
	}

	if tm := exunitTimingRe.FindStringSubmatch(output); tm != nil {
		report.Duration = tm[1] + "s"
	}

	report.Tests = parseExUnitFailures(output)

	return report
}

// parseExUnitFailures scans the output for numbered failure blocks and returns
// one TestResult per block. A block starts at "  N) test/doctest Name (Module)"
// and ends just before the next such header or the summary line.
func parseExUnitFailures(output string) []TestResult {
	lines := strings.Split(output, "\n")
	var results []TestResult

	i := 0
	for i < len(lines) {
		hm := exunitHeaderRe.FindStringSubmatch(lines[i])
		if hm == nil {
			i++
			continue
		}

		tr := TestResult{
			Name:    hm[1],
			Package: hm[2],
			Status:  "failed",
		}

		// Next non-empty line should be the location (file:line)
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i < len(lines) {
			if lm := exunitLocationRe.FindStringSubmatch(lines[i]); lm != nil {
				tr.File = lm[1]
				tr.Line, _ = strconv.Atoi(lm[2])
				i++
			}
		}

		// Collect the body lines.
		// A body line is: blank, OR starts with ≥4 spaces (body is 5-indent,
		// stacktrace entries are 7-indent). Stop at the first non-blank line
		// that has <4 leading spaces — this naturally catches "  * test ..."
		// progress markers, "  N) ..." next-failure headers, bare module
		// headers, and the summary line, all without needing separate regexps.
		var bodyLines []string
		for i < len(lines) {
			line := lines[i]
			if strings.TrimSpace(line) == "" {
				bodyLines = append(bodyLines, "")
				i++
				continue
			}
			// Count leading spaces
			trimmed := strings.TrimLeft(line, " ")
			leading := len(line) - len(trimmed)
			if leading < 4 {
				break
			}
			bodyLines = append(bodyLines, strings.TrimPrefix(line, "     "))
			i++
		}

		// Trim leading/trailing blank lines from the body
		for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[0]) == "" {
			bodyLines = bodyLines[1:]
		}
		for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[len(bodyLines)-1]) == "" {
			bodyLines = bodyLines[:len(bodyLines)-1]
		}

		tr.Message = strings.Join(bodyLines, "\n")
		results = append(results, tr)
	}

	return results
}

func parseMinitest(output string) *TestReport {
	m := minitestSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}

	total, _ := strconv.Atoi(m[1])
	failed, _ := strconv.Atoi(m[2])
	errors, _ := strconv.Atoi(m[3])
	skipped, _ := strconv.Atoi(m[4])

	return &TestReport{
		Framework: "minitest",
		Total:     total,
		Passed:    total - failed - errors - skipped,
		Failed:    failed + errors,
		Skipped:   skipped,
	}
}

// ParseJUnitJSON parses JUnit JSON from Semaphore's test-results CLI.
// Supports two formats:
//   1. Semaphore format: {"testResults": [{suites: [{tests: [...]}]}]}
//   2. Standard JUnit JSON: [{testcases: [...]}]
func ParseJUnitJSON(data []byte) *TestReport {
	// Try Semaphore test-results format first
	if r := parseSemaphoreTestResults(data); r != nil {
		return r
	}
	// Fallback to standard JUnit JSON
	return parseStandardJUnit(data)
}

func parseSemaphoreTestResults(data []byte) *TestReport {
	var wrapper struct {
		TestResults []struct {
			Name      string `json:"name"`
			Framework string `json:"framework"`
			Summary   struct {
				Total   int `json:"total"`
				Passed  int `json:"passed"`
				Failed  int `json:"failed"`
				Skipped int `json:"skipped"`
				Error   int `json:"error"`
			} `json:"summary"`
			Suites []struct {
				Name    string `json:"name"`
				Summary struct {
					Total  int `json:"total"`
					Failed int `json:"failed"`
				} `json:"summary"`
				Tests []struct {
					Name      string `json:"name"`
					File      string `json:"file"`
					Classname string `json:"classname"`
					State     string `json:"state"`
					Duration  int64  `json:"duration"`
					Failure   *struct {
						Message string `json:"message"`
						Body    string `json:"body"`
					} `json:"failure"`
				} `json:"tests"`
			} `json:"suites"`
		} `json:"testResults"`
	}

	if err := json.Unmarshal(data, &wrapper); err != nil || len(wrapper.TestResults) == 0 {
		return nil
	}

	report := &TestReport{}

	for _, tr := range wrapper.TestResults {
		if report.Framework == "" {
			report.Framework = tr.Framework
		}
		report.Total += tr.Summary.Total
		report.Passed += tr.Summary.Passed
		report.Failed += tr.Summary.Failed + tr.Summary.Error
		report.Skipped += tr.Summary.Skipped

		for _, suite := range tr.Suites {
			for _, t := range suite.Tests {
				// Capture every test with its state (passed/failed/skipped),
				// not just failures — flaky detection needs to see a test
				// both pass and fail across runs to flag it.
				res := TestResult{
					Name:    t.Name,
					Package: suite.Name,
					Status:  t.State,
					File:    t.File,
				}
				if t.Failure != nil {
					res.Message = t.Failure.Message
				}
				report.Tests = append(report.Tests, res)
			}
		}
	}

	return report
}

func parseStandardJUnit(data []byte) *TestReport {
	var suites []struct {
		Name      string `json:"name"`
		Tests     int    `json:"tests"`
		Failures  int    `json:"failures"`
		Errors    int    `json:"errors"`
		Skipped   int    `json:"skipped"`
		TestCases []struct {
			Name      string `json:"name"`
			Classname string `json:"classname"`
			Failure   *struct {
				Message string `json:"message"`
			} `json:"failure,omitempty"`
		} `json:"testcases"`
	}

	if err := json.Unmarshal(data, &suites); err != nil {
		return nil
	}

	report := &TestReport{Framework: "junit"}
	for _, suite := range suites {
		report.Total += suite.Tests
		report.Failed += suite.Failures + suite.Errors
		report.Skipped += suite.Skipped
		for _, tc := range suite.TestCases {
			if tc.Failure != nil {
				report.Tests = append(report.Tests, TestResult{
					Name:    tc.Name,
					Package: tc.Classname,
					Status:  "failed",
					Message: tc.Failure.Message,
				})
			}
		}
	}
	report.Passed = report.Total - report.Failed - report.Skipped
	return report
}
