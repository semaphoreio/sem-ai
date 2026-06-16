package testparse

import (
	"strings"
	"testing"
)

// ---- ANSI stripping -------------------------------------------------------------

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "red escape sequence removed",
			input: "\x1b[31mFAIL\x1b[0m",
			want:  "FAIL",
		},
		{
			name:  "bold escape removed",
			input: "\x1b[1mBold\x1b[0m text",
			want:  "Bold text",
		},
		{
			name:  "multiple sequences",
			input: "\x1b[32mPASS\x1b[0m: \x1b[33mwarn\x1b[0m",
			want:  "PASS: warn",
		},
		{
			name:  "no sequences",
			input: "DONE 5 tests, 0 failures in 1.2s",
			want:  "DONE 5 tests, 0 failures in 1.2s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(tc.input)
			if got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---- Go test parsing ------------------------------------------------------------

func TestParseGoTest(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantNil    bool
		framework  string
		total      int
		passed     int
		failed     int
		skipped    int
		duration   string
	}{
		{
			name: "simple all passing",
			input: `=== RUN   TestFoo
--- PASS: TestFoo (0.00s)
DONE 3 tests, 0 failures in 0.553s`,
			framework: "go",
			total:     3,
			passed:    3,
			failed:    0,
			skipped:   0,
			duration:  "0.553s",
		},
		{
			name: "with one failure",
			input: `DONE 11 tests, 1 failure in 1.20s`,
			framework: "go",
			total:     11,
			passed:    10,
			failed:    1,
			skipped:   0,
			duration:  "1.20s",
		},
		{
			name: "plural failures",
			input: `DONE 20 tests, 3 failures in 5.00s`,
			framework: "go",
			total:     20,
			passed:    17,
			failed:    3,
			duration:  "5.00s",
		},
		{
			name: "with skipped",
			input: `DONE 10 tests, 0 failures, 2 skipped in 0.1s`,
			framework: "go",
			total:     10,
			passed:    8,
			failed:    0,
			skipped:   2,
			duration:  "0.1s",
		},
		{
			name:    "no match returns nil",
			input:   "random log output with no test summary",
			wantNil: true,
		},
		{
			name:    "pytest output does not match",
			input:   "5 passed, 1 failed in 2.3s",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGoTest(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != tc.framework {
				t.Errorf("Framework = %q, want %q", got.Framework, tc.framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
			if got.Skipped != tc.skipped {
				t.Errorf("Skipped = %d, want %d", got.Skipped, tc.skipped)
			}
			if tc.duration != "" && got.Duration != tc.duration {
				t.Errorf("Duration = %q, want %q", got.Duration, tc.duration)
			}
		})
	}
}

func TestParseGoTestFailureDetails(t *testing.T) {
	input := `FAIL:   . Test_timeHandler_statusCode (0.00s)
    main_test.go:243: expected status 201, got 200
DONE 5 tests, 1 failure in 0.10s`

	got := parseGoTest(input)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got.Tests) == 0 {
		t.Fatal("expected at least one failed test detail")
	}
	ft := got.Tests[0]
	if ft.Name != "Test_timeHandler_statusCode" {
		t.Errorf("Name = %q, want Test_timeHandler_statusCode", ft.Name)
	}
	if ft.Status != "failed" {
		t.Errorf("Status = %q, want failed", ft.Status)
	}
	if ft.File != "main_test.go" {
		t.Errorf("File = %q, want main_test.go", ft.File)
	}
	if ft.Line != 243 {
		t.Errorf("Line = %d, want 243", ft.Line)
	}
}

func TestParseGoTestWithANSI(t *testing.T) {
	// ANSI codes wrapping the summary line should still parse
	input := "\x1b[32mDONE 4 tests, 0 failures in 0.50s\x1b[0m"
	got := ParseFromLogs(input)
	if got == nil {
		t.Fatal("expected non-nil result after ANSI stripping")
	}
	if got.Total != 4 {
		t.Errorf("Total = %d, want 4", got.Total)
	}
}

// ---- Pytest parsing -------------------------------------------------------------

func TestParsePytest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		total   int
		passed  int
		failed  int
		skipped int
	}{
		{
			name:   "all passed",
			input:  "5 passed in 1.23s",
			total:  5,
			passed: 5,
		},
		{
			name:   "with failures",
			input:  "3 passed, 2 failed in 0.50s",
			total:  5,
			passed: 3,
			failed: 2,
		},
		{
			name:    "with skipped",
			input:   "3 passed, 1 failed, 2 skipped in 2.0s",
			total:   6,
			passed:  3,
			failed:  1,
			skipped: 2,
		},
		{
			name:    "no match",
			input:   "no relevant content",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePytest(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != "pytest" {
				t.Errorf("Framework = %q, want pytest", got.Framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
			if got.Skipped != tc.skipped {
				t.Errorf("Skipped = %d, want %d", got.Skipped, tc.skipped)
			}
		})
	}
}

func TestParsePytestFailureDetails(t *testing.T) {
	input := `FAILED tests/test_foo.py::test_bar - AssertionError: 1 != 2
FAILED tests/test_baz.py::test_qux - ValueError: bad input
2 failed, 5 passed in 3.0s`

	got := parsePytest(input)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got.Tests) != 2 {
		t.Fatalf("expected 2 failed tests, got %d", len(got.Tests))
	}
	if got.Tests[0].Name != "tests/test_foo.py::test_bar" {
		t.Errorf("Name = %q", got.Tests[0].Name)
	}
	if got.Tests[0].Status != "failed" {
		t.Errorf("Status = %q, want failed", got.Tests[0].Status)
	}
}

// ---- RSpec parsing --------------------------------------------------------------

func TestParseRspec(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		total   int
		passed  int
		failed  int
		skipped int
	}{
		{
			name:   "all passed",
			input:  "10 examples, 0 failures",
			total:  10,
			passed: 10,
		},
		{
			name:   "with failures",
			input:  "10 examples, 2 failures",
			total:  10,
			passed: 8,
			failed: 2,
		},
		{
			name:    "with pending",
			input:   "10 examples, 1 failure, 2 pending",
			total:   10,
			passed:  7,
			failed:  1,
			skipped: 2,
		},
		{
			name:    "no match",
			input:   "nothing here",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRspec(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != "rspec" {
				t.Errorf("Framework = %q, want rspec", got.Framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
			if got.Skipped != tc.skipped {
				t.Errorf("Skipped = %d, want %d", got.Skipped, tc.skipped)
			}
		})
	}
}

// ---- Jest parsing ---------------------------------------------------------------

func TestParseJest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		total   int
		passed  int
		failed  int
		skipped int
	}{
		{
			name:   "standard jest output",
			input:  "Tests: 2 failed, 8 passed, 10 total",
			total:  10,
			passed: 8,
			failed: 2,
			skipped: 0,
		},
		{
			name:   "all passing",
			input:  "Tests: 0 failed, 5 passed, 5 total",
			total:  5,
			passed: 5,
			failed: 0,
		},
		{
			name:    "no match",
			input:   "no test output",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseJest(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != "jest" {
				t.Errorf("Framework = %q, want jest", got.Framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
		})
	}
}

// ---- ExUnit parsing -------------------------------------------------------------

func TestParseExUnit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		total    int
		passed   int
		failed   int
		duration string
	}{
		{
			name:  "basic exunit",
			input: "240 tests, 0 failures",
			total: 240,
			passed: 240,
		},
		{
			name:   "with doctest",
			input:  "1 doctest, 240 tests, 0 failures",
			total:  241,
			passed: 241,
		},
		{
			name:   "with failures and timing",
			input:  "100 tests, 3 failures\nFinished in 11.0 seconds",
			total:  100,
			failed: 3,
			passed: 97,
			duration: "11.0s",
		},
		{
			name:    "no match",
			input:   "nothing",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseExUnit(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != "exunit" {
				t.Errorf("Framework = %q, want exunit", got.Framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
			if tc.duration != "" && got.Duration != tc.duration {
				t.Errorf("Duration = %q, want %q", got.Duration, tc.duration)
			}
		})
	}
}

// ---- Minitest parsing -----------------------------------------------------------

func TestParseMinitest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		total   int
		passed  int
		failed  int
		skipped int
	}{
		{
			name:   "all passing",
			input:  "10 runs, 20 assertions, 0 failures, 0 errors, 0 skips",
			total:  10,
			passed: 10,
		},
		{
			name:    "with failures and errors",
			input:   "10 runs, 15 assertions, 1 failures, 2 errors, 1 skips",
			total:   10,
			failed:  3, // 1 failure + 2 errors
			skipped: 1,
			passed:  6,
		},
		{
			name:    "no match",
			input:   "unrelated output",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMinitest(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != "minitest" {
				t.Errorf("Framework = %q, want minitest", got.Framework)
			}
			if got.Total != tc.total {
				t.Errorf("Total = %d, want %d", got.Total, tc.total)
			}
			if got.Passed != tc.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tc.passed)
			}
			if got.Failed != tc.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.failed)
			}
			if got.Skipped != tc.skipped {
				t.Errorf("Skipped = %d, want %d", got.Skipped, tc.skipped)
			}
		})
	}
}

// ---- ParseFromLogs framework dispatch -------------------------------------------

func TestParseFromLogsDispatch(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		framework string
		wantNil   bool
	}{
		{
			name:      "go test",
			input:     "DONE 5 tests, 0 failures in 0.1s",
			framework: "go",
		},
		{
			name:      "pytest",
			input:     "5 passed in 1.0s",
			framework: "pytest",
		},
		{
			name:      "rspec",
			input:     "10 examples, 0 failures",
			framework: "rspec",
		},
		{
			name:      "jest",
			input:     "Tests: 0 failed, 5 passed, 5 total",
			framework: "jest",
		},
		{
			name:      "exunit",
			input:     "100 tests, 0 failures",
			framework: "exunit",
		},
		{
			name:    "unrecognized returns nil",
			input:   "completely unrelated output without any test patterns",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFromLogs(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil for unrecognized output, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.Framework != tc.framework {
				t.Errorf("Framework = %q, want %q", got.Framework, tc.framework)
			}
		})
	}
}

// ---- ParseJUnitJSON -------------------------------------------------------------

func TestParseJUnitJSONSemaphoreFormat(t *testing.T) {
	data := []byte(`{
		"testResults": [{
			"name": "unit",
			"framework": "go",
			"summary": {
				"total": 10,
				"passed": 8,
				"failed": 1,
				"skipped": 1,
				"error": 0
			},
			"suites": [{
				"name": "MySuite",
				"tests": [
					{"name": "TestA", "state": "passed"},
					{"name": "TestB", "state": "failed", "failure": {"message": "expected 1, got 2"}}
				]
			}]
		}]
	}`)

	got := ParseJUnitJSON(data)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Framework != "go" {
		t.Errorf("Framework = %q, want go", got.Framework)
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10", got.Total)
	}
	if got.Passed != 8 {
		t.Errorf("Passed = %d, want 8", got.Passed)
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}

	// Every test is captured with its state (passed and failed alike) —
	// flaky detection needs to see a test both pass and fail across runs.
	if len(got.Tests) != 2 {
		t.Fatalf("expected 2 test details (passed + failed), got %d", len(got.Tests))
	}
	byName := map[string]TestResult{}
	for _, tr := range got.Tests {
		byName[tr.Name] = tr
	}
	if byName["TestA"].Status != "passed" {
		t.Errorf("TestA status = %q, want passed", byName["TestA"].Status)
	}
	if byName["TestB"].Status != "failed" {
		t.Errorf("TestB status = %q, want failed", byName["TestB"].Status)
	}
	if byName["TestB"].Message != "expected 1, got 2" {
		t.Errorf("TestB message = %q", byName["TestB"].Message)
	}
}

// Regression: the Semaphore test-results parser must report a test's passed
// state, not just its failures. Dropping passes broke `test flaky`, which
// flags a test only when it sees it both pass and fail across runs.
func TestParseJUnitJSONCapturesPassedTests(t *testing.T) {
	data := []byte(`{
		"testResults": [{
			"name": "unit",
			"framework": "go",
			"summary": {"total": 1, "passed": 1, "failed": 0, "skipped": 0, "error": 0},
			"suites": [{
				"name": "MySuite",
				"tests": [{"name": "TestFlaky", "state": "passed"}]
			}]
		}]
	}`)

	got := ParseJUnitJSON(data)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got.Tests) != 1 {
		t.Fatalf("expected the passed test to be captured, got %d tests", len(got.Tests))
	}
	if got.Tests[0].Name != "TestFlaky" || got.Tests[0].Status != "passed" {
		t.Errorf("got %q/%q, want TestFlaky/passed", got.Tests[0].Name, got.Tests[0].Status)
	}
}

func TestParseJUnitJSONStandardFormat(t *testing.T) {
	data := []byte(`[{
		"name": "com.example.TestSuite",
		"tests": 5,
		"failures": 1,
		"errors": 0,
		"skipped": 0,
		"testcases": [
			{"name": "testPass", "classname": "com.example.TestSuite"},
			{"name": "testFail", "classname": "com.example.TestSuite", "failure": {"message": "assertion failed"}}
		]
	}]`)

	got := ParseJUnitJSON(data)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Framework != "junit" {
		t.Errorf("Framework = %q, want junit", got.Framework)
	}
	if got.Total != 5 {
		t.Errorf("Total = %d, want 5", got.Total)
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
	if got.Passed != 4 {
		t.Errorf("Passed = %d, want 4", got.Passed)
	}

	if len(got.Tests) != 1 {
		t.Fatalf("expected 1 failed test, got %d", len(got.Tests))
	}
	if got.Tests[0].Name != "testFail" {
		t.Errorf("test name = %q, want testFail", got.Tests[0].Name)
	}
}

func TestParseJUnitJSONInvalidData(t *testing.T) {
	got := ParseJUnitJSON([]byte(`not valid json at all`))
	if got != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", got)
	}
}

// ---- ExUnit failure detail parsing ---------------------------------------------

func TestParseExUnitFailureDetails(t *testing.T) {
	fixture := `
  1) test just_run scheduling implementation schedule() - restarting scheduler task is terminated if periodic is deleted (Scheduler.Actions.ScheduleWfImpl.Test)
     test/actions/schedule_wf_impl_test.exs:542
     match (=) failed
     code:  assert %{workers: 1} = ScheduleTaskManager.count_children()
     left:  %{workers: 1}
     right: %{active: 2, specs: 2, supervisors: 0, workers: 2}
     stacktrace:
       test/actions/schedule_wf_impl_test.exs:566: (test)

  2) test other thing fails (Some.Other.Test)
     test/other_test.exs:10
     Assertion with == failed
     code:  assert foo == bar
     left:  1
     right: 2
     stacktrace:
       test/other_test.exs:12: (test)

Finished in 11.0 seconds
30 doctests, 302 tests, 2 failures
`

	got := ParseFromLogs(fixture)
	if got == nil {
		t.Fatal("expected non-nil report")
	}
	if got.Framework != "exunit" {
		t.Errorf("Framework = %q, want exunit", got.Framework)
	}
	if got.Total != 332 {
		t.Errorf("Total = %d, want 332 (30 doctests + 302 tests)", got.Total)
	}
	if got.Failed != 2 {
		t.Errorf("Failed = %d, want 2", got.Failed)
	}
	if len(got.Tests) != 2 {
		t.Fatalf("expected 2 failure blocks, got %d", len(got.Tests))
	}

	first := got.Tests[0]
	if first.Name != "just_run scheduling implementation schedule() - restarting scheduler task is terminated if periodic is deleted" {
		t.Errorf("Tests[0].Name = %q", first.Name)
	}
	if first.Package != "Scheduler.Actions.ScheduleWfImpl.Test" {
		t.Errorf("Tests[0].Package = %q", first.Package)
	}
	if first.File != "test/actions/schedule_wf_impl_test.exs" {
		t.Errorf("Tests[0].File = %q", first.File)
	}
	if first.Line != 542 {
		t.Errorf("Tests[0].Line = %d, want 542", first.Line)
	}
	if first.Status != "failed" {
		t.Errorf("Tests[0].Status = %q, want failed", first.Status)
	}
	if !strings.Contains(first.Message, "left:  %{workers: 1}") {
		t.Errorf("Tests[0].Message missing left assertion, got: %q", first.Message)
	}
	if !strings.Contains(first.Message, "right: %{active: 2, specs: 2, supervisors: 0, workers: 2}") {
		t.Errorf("Tests[0].Message missing right assertion, got: %q", first.Message)
	}

	second := got.Tests[1]
	if second.Name != "other thing fails" {
		t.Errorf("Tests[1].Name = %q", second.Name)
	}
	if second.Package != "Some.Other.Test" {
		t.Errorf("Tests[1].Package = %q", second.Package)
	}
	if second.File != "test/other_test.exs" {
		t.Errorf("Tests[1].File = %q", second.File)
	}
	if second.Line != 10 {
		t.Errorf("Tests[1].Line = %d, want 10", second.Line)
	}
}

func TestParseExUnitNoFailuresEmptyTests(t *testing.T) {
	input := "240 tests, 0 failures\nFinished in 5.0 seconds"
	got := ParseFromLogs(input)
	if got == nil {
		t.Fatal("expected non-nil report")
	}
	if len(got.Tests) != 0 {
		t.Errorf("expected no Tests entries for zero-failure run, got %d", len(got.Tests))
	}
}

// Regression: failure body must stop at the first line with <4 leading spaces
// (progress markers "  * test ...", module headers, etc.) and must NOT swallow
// the rest of the suite output up to the summary line.
func TestParseExUnitFailureBodyBounded(t *testing.T) {
	fixture := `
  1) test alpha fails (My.Test)
     test/my_test.exs:10
     match (=) failed
     code:  assert a == b
     left:  1
     right: 2
     stacktrace:
       test/my_test.exs:12: (test)

  * test alpha fails (12.3ms) [L#10]
  * test beta passes (4.1ms) [L#20]

My.Other.Test [test/other_test.exs]
  * test gamma passes (1.0ms) [L#5]

Finished in 5.0 seconds
1 doctest, 50 tests, 1 failure
`

	got := ParseFromLogs(fixture)
	if got == nil {
		t.Fatal("expected non-nil report")
	}
	if len(got.Tests) != 1 {
		t.Fatalf("expected 1 failure block, got %d", len(got.Tests))
	}

	msg := got.Tests[0].Message
	if !strings.Contains(msg, "left:  1") {
		t.Errorf("message missing 'left:  1', got: %q", msg)
	}
	if !strings.Contains(msg, "right: 2") {
		t.Errorf("message missing 'right: 2', got: %q", msg)
	}
	if !strings.Contains(msg, "stacktrace:") {
		t.Errorf("message missing 'stacktrace:', got: %q", msg)
	}
	if strings.Contains(msg, "test beta passes") {
		t.Errorf("message must not contain progress line 'test beta passes', got: %q", msg)
	}
	if strings.Contains(msg, "My.Other.Test") {
		t.Errorf("message must not contain module header 'My.Other.Test', got: %q", msg)
	}
	if strings.Contains(msg, "test gamma passes") {
		t.Errorf("message must not contain unrelated test 'test gamma passes', got: %q", msg)
	}
	if strings.Contains(msg, "Finished in") {
		t.Errorf("message must not contain summary 'Finished in', got: %q", msg)
	}
}

func TestParseJUnitJSONEmptySemaphoreWrapper(t *testing.T) {
	// Valid JSON but empty testResults → falls through to standard JUnit
	data := []byte(`{"testResults": []}`)
	// Standard JUnit parser also won't match this (it's not an array)
	// so result may be nil — that's acceptable behaviour
	_ = ParseJUnitJSON(data)
}

func TestParseJUnitJSONMultipleSuites(t *testing.T) {
	data := []byte(`[
		{"name": "Suite1", "tests": 3, "failures": 1, "errors": 0, "skipped": 0, "testcases": []},
		{"name": "Suite2", "tests": 7, "failures": 2, "errors": 1, "skipped": 1, "testcases": []}
	]`)

	got := ParseJUnitJSON(data)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10", got.Total)
	}
	if got.Failed != 4 { // 1 + 2 + 1 (error counts as failure)
		t.Errorf("Failed = %d, want 4", got.Failed)
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
}
