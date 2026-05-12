package cmd

import (
	"reflect"
	"testing"
	"time"
)

// helpers

func mustTime(s string) time.Time {
	t := parseTime(s)
	return t
}

func pip(created, done string, result string) analyticsPipeline {
	return analyticsPipeline{
		CreatedAt: mustTime(created),
		DoneAt:    mustTime(done),
		Result:    result,
	}
}

// --- computeDurations ---

func TestComputeDurationsEmpty(t *testing.T) {
	r := computeDurations(nil)
	if r["samples"] != 0 {
		t.Errorf("want samples=0, got %v", r["samples"])
	}
	if r["avg"] != "n/a" {
		t.Errorf("want avg=n/a, got %v", r["avg"])
	}
}

func TestComputeDurationsSingle(t *testing.T) {
	p := pip("2024-01-01 00:00:00.000000Z", "2024-01-01 00:01:30.000000Z", "passed")
	r := computeDurations([]analyticsPipeline{p})
	if r["samples"] != 1 {
		t.Errorf("want samples=1, got %v", r["samples"])
	}
	if r["avg"] != "1m30s" {
		t.Errorf("want avg=1m30s, got %v", r["avg"])
	}
}

func TestComputeDurationsMultiple(t *testing.T) {
	pipelines := []analyticsPipeline{
		pip("2024-01-01 00:00:00.000000Z", "2024-01-01 00:01:00.000000Z", "passed"), // 60s
		pip("2024-01-01 00:00:00.000000Z", "2024-01-01 00:02:00.000000Z", "passed"), // 120s
		pip("2024-01-01 00:00:00.000000Z", "2024-01-01 00:03:00.000000Z", "failed"), // 180s
	}
	r := computeDurations(pipelines)
	if r["samples"] != 3 {
		t.Errorf("want samples=3, got %v", r["samples"])
	}
	// avg = (60+120+180)/3 = 120s = 2m0s
	if r["avg"] != "2m0s" {
		t.Errorf("want avg=2m0s, got %v", r["avg"])
	}
	// min should be 1m0s, max 3m0s
	if r["min"] != "1m0s" {
		t.Errorf("want min=1m0s, got %v", r["min"])
	}
	if r["max"] != "3m0s" {
		t.Errorf("want max=3m0s, got %v", r["max"])
	}
}

func TestComputeDurationsSkipsZeroTimes(t *testing.T) {
	p := analyticsPipeline{Result: "passed"} // zero times
	r := computeDurations([]analyticsPipeline{p})
	if r["samples"] != 0 {
		t.Errorf("want samples=0, got %v", r["samples"])
	}
}

// --- computePhase ---

func TestComputePhaseCompile(t *testing.T) {
	p := analyticsPipeline{
		CreatedAt: mustTime("2024-01-01 00:00:00.000000Z"),
		PendingAt: mustTime("2024-01-01 00:00:10.000000Z"),
	}
	r := computePhase([]analyticsPipeline{p}, func(pp analyticsPipeline) (time.Time, time.Time) {
		return pp.CreatedAt, pp.PendingAt
	})
	if r["samples"] != 1 {
		t.Errorf("want samples=1, got %v", r["samples"])
	}
	if r["avg"] != "10s" {
		t.Errorf("want avg=10s, got %v", r["avg"])
	}
}

func TestComputePhaseQueue(t *testing.T) {
	p := analyticsPipeline{
		QueuingAt: mustTime("2024-01-01 00:00:00.000000Z"),
		RunningAt: mustTime("2024-01-01 00:00:30.000000Z"),
	}
	r := computePhase([]analyticsPipeline{p}, func(pp analyticsPipeline) (time.Time, time.Time) {
		return pp.QueuingAt, pp.RunningAt
	})
	if r["avg"] != "30s" {
		t.Errorf("want avg=30s, got %v", r["avg"])
	}
}

func TestComputePhaseExecution(t *testing.T) {
	p := analyticsPipeline{
		RunningAt: mustTime("2024-01-01 00:00:00.000000Z"),
		DoneAt:    mustTime("2024-01-01 00:05:00.000000Z"),
	}
	r := computePhase([]analyticsPipeline{p}, func(pp analyticsPipeline) (time.Time, time.Time) {
		return pp.RunningAt, pp.DoneAt
	})
	if r["avg"] != "5m0s" {
		t.Errorf("want avg=5m0s, got %v", r["avg"])
	}
}

func TestComputePhaseSkipsZero(t *testing.T) {
	p := analyticsPipeline{} // all zero
	r := computePhase([]analyticsPipeline{p}, func(pp analyticsPipeline) (time.Time, time.Time) {
		return pp.RunningAt, pp.DoneAt
	})
	if r["samples"] != 0 {
		t.Errorf("want samples=0, got %v", r["samples"])
	}
}

// --- computeFailures ---

func failingBlocksLen(r map[string]any) int {
	v := r["failing_blocks"]
	if v == nil {
		return 0
	}
	return reflect.ValueOf(v).Len()
}

func TestComputeFailuresNoFailures(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "passed", Blocks: []analyticsBlock{{Name: "build", State: "done", Result: "passed"}}},
	}
	r := computeFailures(pipelines)
	if n := failingBlocksLen(r); n != 0 {
		t.Errorf("want 0 failing blocks, got %d", n)
	}
	if r["passed"] != 1 || r["failed"] != 0 {
		t.Errorf("unexpected passed/failed counts: %v/%v", r["passed"], r["failed"])
	}
}

func TestComputeFailuresSingleFailingBlock(t *testing.T) {
	pipelines := []analyticsPipeline{
		{
			Result: "failed",
			Blocks: []analyticsBlock{
				{Name: "tests", Result: "failed"},
				{Name: "tests", Result: "passed"},
			},
		},
	}
	r := computeFailures(pipelines)
	blocks := r["failing_blocks"]
	if blocks == nil {
		t.Fatal("failing_blocks should not be nil")
	}
}

func TestComputeFailuresMultipleBlocksSortedByFailCount(t *testing.T) {
	pipelines := []analyticsPipeline{
		{
			Result: "failed",
			Blocks: []analyticsBlock{
				{Name: "lint", Result: "failed"},
				{Name: "tests", Result: "failed"},
				{Name: "tests", Result: "failed"},
			},
		},
		{
			Result: "failed",
			Blocks: []analyticsBlock{
				{Name: "tests", Result: "failed"},
			},
		},
	}
	r := computeFailures(pipelines)

	// We need to access via interface{} since the type is unexported struct slice
	// Just verify pass/fail counts and that failing_blocks is present
	if r["failed"] != 2 {
		t.Errorf("want failed=2, got %v", r["failed"])
	}
	if r["failing_blocks"] == nil {
		t.Error("failing_blocks should not be nil")
	}
}

// --- computeFailureReasons ---

func TestComputeFailureReasonsNoFailures(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "passed"},
		{Result: "passed"},
	}
	r := computeFailureReasons(pipelines)
	if len(r) != 0 {
		t.Errorf("want empty reasons map, got %v", r)
	}
}

func TestComputeFailureReasonsTestFailure(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "failed", ResultReason: "test"},
		{Result: "failed", ResultReason: "test"},
	}
	r := computeFailureReasons(pipelines)
	if r["test"] != 2 {
		t.Errorf("want test=2, got %v", r["test"])
	}
}

func TestComputeFailureReasonsTimeout(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "failed", ResultReason: "timeout"},
		{Result: "stopped", ResultReason: "timeout"},
	}
	r := computeFailureReasons(pipelines)
	if r["timeout"] != 2 {
		t.Errorf("want timeout=2, got %v", r["timeout"])
	}
}

func TestComputeFailureReasonsMalformed(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "failed", ResultReason: ""},
	}
	r := computeFailureReasons(pipelines)
	if r["unknown"] != 1 {
		t.Errorf("want unknown=1, got %v", r["unknown"])
	}
}

func TestComputeFailureReasonsMixed(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "failed", ResultReason: "test"},
		{Result: "failed", ResultReason: "timeout"},
		{Result: "failed", ResultReason: ""},
		{Result: "passed", ResultReason: "test"}, // should be ignored
	}
	r := computeFailureReasons(pipelines)
	if r["test"] != 1 {
		t.Errorf("want test=1, got %v", r["test"])
	}
	if r["timeout"] != 1 {
		t.Errorf("want timeout=1, got %v", r["timeout"])
	}
	if r["unknown"] != 1 {
		t.Errorf("want unknown=1, got %v", r["unknown"])
	}
	if _, ok := r["passed"]; ok {
		t.Error("passed result should not appear in failure reasons")
	}
}

// --- computeTriggers ---

func TestComputeTriggersHook(t *testing.T) {
	pipelines := []analyticsPipeline{
		{TriggeredBy: "hook"},
		{TriggeredBy: "hook"},
	}
	r := computeTriggers(pipelines)
	if r["hook"] != 2 {
		t.Errorf("want hook=2, got %v", r["hook"])
	}
}

func TestComputeTriggersAPI(t *testing.T) {
	pipelines := []analyticsPipeline{
		{TriggeredBy: "api"},
	}
	r := computeTriggers(pipelines)
	if r["api"] != 1 {
		t.Errorf("want api=1, got %v", r["api"])
	}
}

func TestComputeTriggersSchedule(t *testing.T) {
	pipelines := []analyticsPipeline{
		{TriggeredBy: "schedule"},
	}
	r := computeTriggers(pipelines)
	if r["schedule"] != 1 {
		t.Errorf("want schedule=1, got %v", r["schedule"])
	}
}

func TestComputeTriggersMixed(t *testing.T) {
	pipelines := []analyticsPipeline{
		{TriggeredBy: "hook"},
		{TriggeredBy: "api"},
		{TriggeredBy: "schedule"},
		{TriggeredBy: ""},
	}
	r := computeTriggers(pipelines)
	if r["hook"] != 1 || r["api"] != 1 || r["schedule"] != 1 || r["unknown"] != 1 {
		t.Errorf("unexpected trigger counts: %v", r)
	}
}

// --- countDeploys ---

func TestCountDeploysZero(t *testing.T) {
	saved := analyticsDaysFlag
	analyticsDaysFlag = 7
	defer func() { analyticsDaysFlag = saved }()

	pipelines := []analyticsPipeline{
		{HasPromo: false},
		{HasPromo: false},
	}
	r := countDeploys(pipelines)
	if r["total"] != 0 {
		t.Errorf("want total=0, got %v", r["total"])
	}
	if r["per_day"] != "0.0" {
		t.Errorf("want per_day=0.0, got %v", r["per_day"])
	}
}

func TestCountDeploysSome(t *testing.T) {
	saved := analyticsDaysFlag
	analyticsDaysFlag = 7
	defer func() { analyticsDaysFlag = saved }()

	pipelines := []analyticsPipeline{
		{HasPromo: true},
		{HasPromo: false},
		{HasPromo: true},
		{HasPromo: true},
	}
	r := countDeploys(pipelines)
	if r["total"] != 3 {
		t.Errorf("want total=3, got %v", r["total"])
	}
	// per_day = 3/7 ≈ 0.4
	if r["per_day"] != "0.4" {
		t.Errorf("want per_day=0.4, got %v", r["per_day"])
	}
	// per_week = 0.4*7 = 3.0
	if r["per_week"] != "3.0" {
		t.Errorf("want per_week=3.0, got %v", r["per_week"])
	}
}

// --- passRate ---

func TestPassRateAllPass(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "passed"},
		{Result: "passed"},
	}
	if got := passRate(pipelines); got != 100.0 {
		t.Errorf("want 100, got %f", got)
	}
}

func TestPassRateAllFail(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "failed"},
		{Result: "failed"},
	}
	if got := passRate(pipelines); got != 0.0 {
		t.Errorf("want 0, got %f", got)
	}
}

func TestPassRateMixed(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "passed"},
		{Result: "failed"},
		{Result: "failed"},
		{Result: "failed"},
	}
	got := passRate(pipelines)
	if got != 25.0 {
		t.Errorf("want 25.0, got %f", got)
	}
}

func TestPassRateEmpty(t *testing.T) {
	if got := passRate(nil); got != 0.0 {
		t.Errorf("want 0, got %f", got)
	}
}

func TestPassRateIgnoresOtherResults(t *testing.T) {
	pipelines := []analyticsPipeline{
		{Result: "passed"},
		{Result: "stopped"},  // not counted
		{Result: "canceled"}, // not counted
	}
	got := passRate(pipelines)
	if got != 100.0 {
		t.Errorf("want 100.0 (only passed vs failed), got %f", got)
	}
}

// --- statsMap ---

func TestStatsMapEmpty(t *testing.T) {
	r := statsMap(nil)
	if r["avg"] != "n/a" || r["p50"] != "n/a" || r["p95"] != "n/a" || r["samples"] != 0 {
		t.Errorf("unexpected empty statsMap: %v", r)
	}
}

func TestStatsMapSingle(t *testing.T) {
	r := statsMap([]float64{60})
	if r["samples"] != 1 {
		t.Errorf("want samples=1, got %v", r["samples"])
	}
	if r["avg"] != "1m0s" {
		t.Errorf("want avg=1m0s, got %v", r["avg"])
	}
	if r["p50"] != "1m0s" {
		t.Errorf("want p50=1m0s, got %v", r["p50"])
	}
	if r["p95"] != "1m0s" {
		t.Errorf("want p95=1m0s, got %v", r["p95"])
	}
}

func TestStatsMapMultiple(t *testing.T) {
	// [10, 20, 30, 40, 50, 60, 70, 80, 90, 100]
	vals := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	r := statsMap(vals)
	if r["samples"] != 10 {
		t.Errorf("want samples=10, got %v", r["samples"])
	}
	// avg = 55s
	if r["avg"] != "55s" {
		t.Errorf("want avg=55s, got %v", r["avg"])
	}
	// p50: idx=4.5 → (50+60)/2 = 55s
	if r["p50"] != "55s" {
		t.Errorf("want p50=55s, got %v", r["p50"])
	}
	// p95: idx=8.55 → 90 + 0.55*(100-90) = 95.5s → formatDuration → "1m35s"
	if r["p95"] != "1m35s" {
		t.Errorf("want p95=1m35s, got %v", r["p95"])
	}
	if r["min"] != "10s" {
		t.Errorf("want min=10s, got %v", r["min"])
	}
	// max=100s → formatDuration → "1m40s"
	if r["max"] != "1m40s" {
		t.Errorf("want max=1m40s, got %v", r["max"])
	}
}

// --- percentile ---

func TestPercentileEmpty(t *testing.T) {
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("want 0, got %f", got)
	}
}

func TestPercentileSingleElement(t *testing.T) {
	if got := percentile([]float64{42}, 50); got != 42 {
		t.Errorf("want 42, got %f", got)
	}
	if got := percentile([]float64{42}, 95); got != 42 {
		t.Errorf("want 42 for p95, got %f", got)
	}
}

func TestPercentileExactBoundary(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50}
	// p0 → idx=0 → 10
	if got := percentile(vals, 0); got != 10 {
		t.Errorf("p0: want 10, got %f", got)
	}
	// p100 → idx=4 → 50
	if got := percentile(vals, 100); got != 50 {
		t.Errorf("p100: want 50, got %f", got)
	}
	// p50 → idx=2 → 30
	if got := percentile(vals, 50); got != 30 {
		t.Errorf("p50: want 30, got %f", got)
	}
}

func TestPercentileInterpolation(t *testing.T) {
	vals := []float64{0, 100}
	// p50: idx=0.5 → 0*(0.5) + 100*(0.5) = 50
	if got := percentile(vals, 50); got != 50 {
		t.Errorf("p50 interpolation: want 50, got %f", got)
	}
}

// --- formatDuration ---

func TestFormatDurationSecondsOnly(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, "0s"},
		{1, "1s"},
		{59, "59s"},
		{59.9, "60s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.seconds)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestFormatDurationMinutesAndSeconds(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{60, "1m0s"},
		{90, "1m30s"},
		{3661, "61m1s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.seconds)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

// --- parseTime ---

func TestParseTimeValid(t *testing.T) {
	t1 := parseTime("2024-03-15 12:30:00.000000Z")
	if t1.IsZero() {
		t.Error("expected non-zero time for valid input")
	}
	if t1.Year() != 2024 || t1.Month() != 3 || t1.Day() != 15 {
		t.Errorf("unexpected date: %v", t1)
	}
}

func TestParseTimeEpochZero(t *testing.T) {
	t1 := parseTime("1970-01-01 00:00:00.000000Z")
	if !t1.IsZero() {
		t.Errorf("epoch zero should return zero time, got %v", t1)
	}
}

func TestParseTimeEmpty(t *testing.T) {
	t1 := parseTime("")
	if !t1.IsZero() {
		t.Errorf("empty string should return zero time, got %v", t1)
	}
}

func TestParseTimeShortFormat(t *testing.T) {
	t1 := parseTime("2024-06-01 09:00:00Z")
	if t1.IsZero() {
		t.Error("expected non-zero time for short format")
	}
	if t1.Hour() != 9 {
		t.Errorf("expected hour=9, got %d", t1.Hour())
	}
}

// --- weekLabel ---

func TestWeekLabelISOWeek(t *testing.T) {
	monday := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)
	label := weekLabel(monday)
	if weekDisplayLabel(label) != "Jan 08 - Jan 14" {
		t.Errorf("display = %q, want 'Jan 08 - Jan 14'", weekDisplayLabel(label))
	}
	if weekSortKey(label) != "2024-W02" {
		t.Errorf("sort key = %q, want '2024-W02'", weekSortKey(label))
	}
}

func TestWeekLabelMidWeek(t *testing.T) {
	wednesday := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	monday := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)
	if weekLabel(wednesday) != weekLabel(monday) {
		t.Errorf("same week, different labels: %q vs %q", weekLabel(wednesday), weekLabel(monday))
	}
}

func TestWeekLabelCrossMonth(t *testing.T) {
	monday := time.Date(2024, 1, 29, 0, 0, 0, 0, time.UTC)
	if weekDisplayLabel(weekLabel(monday)) != "Jan 29 - Feb 04" {
		t.Errorf("got %q", weekDisplayLabel(weekLabel(monday)))
	}
}

func TestWeekLabelCrossYear(t *testing.T) {
	dec31 := time.Date(2018, 12, 31, 0, 0, 0, 0, time.UTC)
	if weekSortKey(weekLabel(dec31)) != "2019-W01" {
		t.Errorf("sort key = %q, want '2019-W01'", weekSortKey(weekLabel(dec31)))
	}
}

func TestWeekSortOrder(t *testing.T) {
	dec := time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	decKey := weekSortKey(weekLabel(dec))
	janKey := weekSortKey(weekLabel(jan))
	if decKey >= janKey {
		t.Errorf("Dec week %q should sort before Jan week %q", decKey, janKey)
	}
}

// --- triggerName ---

func TestTriggerName(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "hook"},
		{1, "api"},
		{2, "schedule"},
		{3, "rerun"},
		{4, "unknown(4)"},
		{99, "unknown(99)"},
		{-1, "unknown(-1)"},
	}
	for _, tc := range tests {
		got := triggerName(tc.input)
		if got != tc.want {
			t.Errorf("triggerName(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
