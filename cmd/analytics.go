package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var (
	analyticsProjectFlag string
	analyticsBranchFlag  string
	analyticsDaysFlag    int
	analyticsLimitFlag   int
	analyticsWeeksFlag   int
)

var analyticsCmd = &cobra.Command{
	Use:   "analytics",
	Short: "Pipeline and workflow analytics over time",
}

// --- analytics summary ---

var analyticsSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "All-in-one analytics overview for a project",
	Example: `  sem-ai analytics summary --project my-app --days 7
  sem-ai analytics summary --project my-app --days 30 --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found", "project": analyticsProjectFlag})
			return nil
		}

		output.Result(map[string]any{
			"project":   analyticsProjectFlag,
			"branch":    analyticsBranchFlag,
			"period":    fmt.Sprintf("%dd", analyticsDaysFlag),
			"workflows": len(pipelines),
			"pass_rate": fmtPct(passRate(pipelines)),
			"duration":  computeDurations(pipelines),
			"compile":   computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.CreatedAt, p.PendingAt }),
			"queue":     computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.QueuingAt, p.RunningAt }),
			"execution": computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.RunningAt, p.DoneAt }),
			"failures":  computeFailures(pipelines),
			"deploys":   countDeploys(pipelines),
			"triggers":  computeTriggers(pipelines),
		})
		return nil
	},
}

// --- analytics duration ---

var analyticsDurationCmd = &cobra.Command{
	Use:     "duration",
	Short:   "Pipeline duration trends (avg, p50, p95)",
	Example: `  sem-ai analytics duration --project my-app --days 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found"})
			return nil
		}

		r := computeDurations(pipelines)
		r["project"] = analyticsProjectFlag
		r["period"] = fmt.Sprintf("%dd", analyticsDaysFlag)
		r["pipelines"] = len(pipelines)
		r["phases"] = map[string]any{
			"compile":   computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.CreatedAt, p.PendingAt }),
			"queue":     computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.QueuingAt, p.RunningAt }),
			"execution": computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.RunningAt, p.DoneAt }),
		}
		output.Result(r)
		return nil
	},
}

// --- analytics failures ---

var analyticsFailuresCmd = &cobra.Command{
	Use:     "failures",
	Short:   "Block-level failure rates",
	Example: `  sem-ai analytics failures --project my-app --days 14`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found"})
			return nil
		}

		r := computeFailures(pipelines)
		r["project"] = analyticsProjectFlag
		r["period"] = fmt.Sprintf("%dd", analyticsDaysFlag)
		r["pipelines"] = len(pipelines)
		r["failure_reasons"] = computeFailureReasons(pipelines)
		output.Result(r)
		return nil
	},
}

// --- analytics queue ---

var analyticsQueueCmd = &cobra.Command{
	Use:     "queue",
	Short:   "Queue wait time analysis",
	Example: `  sem-ai analytics queue --project my-app --days 7`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found"})
			return nil
		}

		r := computePhase(pipelines, func(p analyticsPipeline) (time.Time, time.Time) { return p.QueuingAt, p.RunningAt })
		r["project"] = analyticsProjectFlag
		r["period"] = fmt.Sprintf("%dd", analyticsDaysFlag)
		r["pipelines"] = len(pipelines)
		output.Result(r)
		return nil
	},
}

// --- analytics deploys ---

var analyticsDeploysCmd = &cobra.Command{
	Use:     "deploys",
	Short:   "Deploy frequency and promotion stats",
	Example: `  sem-ai analytics deploys --project my-app --days 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found"})
			return nil
		}

		r := countDeploys(pipelines)
		r["project"] = analyticsProjectFlag
		r["period"] = fmt.Sprintf("%dd", analyticsDaysFlag)
		output.Result(r)
		return nil
	},
}

// --- analytics trend ---

var analyticsTrendCmd = &cobra.Command{
	Use:   "trend",
	Short: "Week-over-week trends for duration, pass rate, queue time, and failure reasons",
	Example: `  sem-ai analytics trend --project my-app --weeks 4
  sem-ai analytics trend --project my-app --weeks 8 --branch main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		analyticsDaysFlag = analyticsWeeksFlag * 7
		if analyticsLimitFlag <= 0 {
			analyticsLimitFlag = analyticsWeeksFlag * 50 // ~50 workflows per week max
		}

		pipelines, err := fetchAnalyticsPipelines()
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			output.Result(map[string]any{"message": "no pipelines found", "project": analyticsProjectFlag})
			return nil
		}

		// Bucket by week (Monday-Sunday)
		buckets := map[string][]analyticsPipeline{}
		var weekKeys []string
		weekKeySeen := map[string]bool{}

		for _, p := range pipelines {
			if p.CreatedAt.IsZero() {
				continue
			}
			wk := weekLabel(p.CreatedAt)
			buckets[wk] = append(buckets[wk], p)
			if !weekKeySeen[wk] {
				weekKeys = append(weekKeys, wk)
				weekKeySeen[wk] = true
			}
		}

		sort.Slice(weekKeys, func(i, j int) bool {
			return weekSortKey(weekKeys[i]) < weekSortKey(weekKeys[j])
		})

		var weeks []map[string]any
		for _, wk := range weekKeys {
			ppls := buckets[wk]
			w := map[string]any{
				"week":            weekDisplayLabel(wk),
				"workflows":       len(ppls),
				"pass_rate":       fmtPct(passRate(ppls)),
				"duration":        computeDurations(ppls),
				"compile":         computePhase(ppls, func(p analyticsPipeline) (time.Time, time.Time) { return p.CreatedAt, p.PendingAt }),
				"queue":           computePhase(ppls, func(p analyticsPipeline) (time.Time, time.Time) { return p.QueuingAt, p.RunningAt }),
				"execution":       computePhase(ppls, func(p analyticsPipeline) (time.Time, time.Time) { return p.RunningAt, p.DoneAt }),
				"failure_reasons": computeFailureReasons(ppls),
				"triggers":        computeTriggers(ppls),
			}
			weeks = append(weeks, w)
		}

		// Compute trend direction
		trend := "stable"
		if len(weeks) >= 2 {
			first := passRate(buckets[weekKeys[0]])
			last := passRate(buckets[weekKeys[len(weekKeys)-1]])
			if last-first > 10 {
				trend = "improving"
			} else if first-last > 10 {
				trend = "degrading"
			}
		}

		output.Result(map[string]any{
			"project": analyticsProjectFlag,
			"branch":  analyticsBranchFlag,
			"weeks":   weeks,
			"trend":   trend,
			"total":   len(pipelines),
		})
		return nil
	},
}

// --- data types ---

type analyticsPipeline struct {
	ID            string
	Name          string
	State         string
	Result        string
	ResultReason  string
	CreatedAt     time.Time
	PendingAt     time.Time
	QueuingAt     time.Time
	RunningAt     time.Time
	DoneAt        time.Time
	TriggeredBy   string
	CommitSHA     string
	CommitMessage string
	RerunOf       string
	Blocks        []analyticsBlock
	HasPromo      bool
}

type analyticsBlock struct {
	Name   string
	State  string
	Result string
}

// --- data fetching ---

func fetchAnalyticsPipelines() ([]analyticsPipeline, error) {
	if !config.IsConfigured() {
		return nil, fmt.Errorf("not configured; run 'sem-ai connect' first")
	}

	project := analyticsProjectFlag
	if project == "" {
		p, err := detectProject()
		if err != nil {
			output.Error("context_error", "could not detect project; use --project", 1)
			return nil, err
		}
		project = p
		analyticsProjectFlag = project
	}

	projectID, err := resolveProjectID(project)
	if err != nil {
		output.Error("project_error", err.Error(), 1)
		return nil, err
	}

	c := client.New()

	// Fetch workflows with pagination for larger windows
	params := url.Values{}
	params.Set("project_id", projectID)
	if analyticsBranchFlag != "" {
		params.Set("branch_name", analyticsBranchFlag)
	}

	type wfEntry struct {
		WfID         string `json:"wf_id"`
		InitialPplID string `json:"initial_ppl_id"`
		TriggeredBy  int    `json:"triggered_by"`
		RerunOf      string `json:"rerun_of"`
		CreatedAt    struct {
			Seconds int64 `json:"seconds"`
		} `json:"created_at"`
	}

	cutoff := time.Now().AddDate(0, 0, -analyticsDaysFlag)
	var filtered []wfEntry

	pagesFetched := 0
	maxPages := analyticsDaysFlag/7 + 5 // rough estimate: ~1 page per week + buffer
	if maxPages < 5 {
		maxPages = 5
	}

	stopWhenPastCutoff := func(page []json.RawMessage) bool {
		pagesFetched++
		if pagesFetched >= maxPages {
			return true
		}
		for _, raw := range page {
			var wf wfEntry
			if json.Unmarshal(raw, &wf) == nil {
				t := time.Unix(wf.CreatedAt.Seconds, 0)
				if t.Before(cutoff) {
					return true
				}
			}
		}
		return false
	}

	allWfs, err := c.ListAll("plumber-workflows", params, stopWhenPastCutoff)
	if err != nil {
		output.Error("api_error", err.Error(), 1)
		return nil, err
	}

	for _, raw := range allWfs {
		var wf wfEntry
		if json.Unmarshal(raw, &wf) != nil {
			continue
		}
		t := time.Unix(wf.CreatedAt.Seconds, 0)
		if t.After(cutoff) {
			filtered = append(filtered, wf)
		}
	}

	// Apply limit
	if analyticsLimitFlag > 0 && len(filtered) > analyticsLimitFlag {
		filtered = filtered[:analyticsLimitFlag]
	}

	if len(filtered) == 0 {
		return nil, nil
	}

	// Fetch pipeline details in parallel
	type pplResult struct {
		idx int
		ppl analyticsPipeline
		err error
	}

	results := make([]analyticsPipeline, len(filtered))
	ch := make(chan pplResult, len(filtered))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for i, wf := range filtered {
		wg.Add(1)
		go func(idx int, wf wfEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ppl, fetchErr := fetchPipelineAnalytics(c, wf.InitialPplID)
			if fetchErr == nil {
				ppl.TriggeredBy = triggerName(wf.TriggeredBy)
				ppl.RerunOf = wf.RerunOf
			}
			ch <- pplResult{idx: idx, ppl: ppl, err: fetchErr}
		}(i, wf)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		if r.err == nil {
			results[r.idx] = r.ppl
		}
	}

	var pipelines []analyticsPipeline
	for _, p := range results {
		if p.ID != "" {
			pipelines = append(pipelines, p)
		}
	}

	return pipelines, nil
}

func fetchPipelineAnalytics(c *client.Client, pplID string) (analyticsPipeline, error) {
	params := url.Values{}
	params.Set("detailed", "true")
	resp, err := c.ListWithParams("pipelines/"+pplID, params)
	if err != nil {
		return analyticsPipeline{}, err
	}
	if resp.StatusCode != 200 {
		return analyticsPipeline{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var data struct {
		Pipeline struct {
			PplID         string `json:"ppl_id"`
			Name          string `json:"name"`
			State         string `json:"state"`
			Result        string `json:"result"`
			ResultReason  string `json:"result_reason"`
			CreatedAt     string `json:"created_at"`
			PendingAt     string `json:"pending_at"`
			QueuingAt     string `json:"queuing_at"`
			RunningAt     string `json:"running_at"`
			DoneAt        string `json:"done_at"`
			PromotionOf   string `json:"promotion_of"`
			CommitSHA     string `json:"commit_sha"`
			CommitMessage string `json:"commit_message"`
		} `json:"pipeline"`
		Blocks []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Result string `json:"result"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		return analyticsPipeline{}, err
	}

	ppl := analyticsPipeline{
		ID:            data.Pipeline.PplID,
		Name:          data.Pipeline.Name,
		State:         data.Pipeline.State,
		Result:        data.Pipeline.Result,
		ResultReason:  data.Pipeline.ResultReason,
		CreatedAt:     parseTime(data.Pipeline.CreatedAt),
		PendingAt:     parseTime(data.Pipeline.PendingAt),
		QueuingAt:     parseTime(data.Pipeline.QueuingAt),
		RunningAt:     parseTime(data.Pipeline.RunningAt),
		DoneAt:        parseTime(data.Pipeline.DoneAt),
		CommitSHA:     data.Pipeline.CommitSHA,
		CommitMessage: data.Pipeline.CommitMessage,
		HasPromo:      data.Pipeline.PromotionOf != "",
	}

	for _, b := range data.Blocks {
		ppl.Blocks = append(ppl.Blocks, analyticsBlock{
			Name:   b.Name,
			State:  b.State,
			Result: b.Result,
		})
	}

	return ppl, nil
}

// --- computation ---

func computeDurations(pipelines []analyticsPipeline) map[string]any {
	var durations []float64
	for _, p := range pipelines {
		if p.DoneAt.IsZero() || p.CreatedAt.IsZero() {
			continue
		}
		d := p.DoneAt.Sub(p.CreatedAt).Seconds()
		if d > 0 {
			durations = append(durations, d)
		}
	}
	return statsMap(durations)
}

func computePhase(pipelines []analyticsPipeline, extract func(analyticsPipeline) (time.Time, time.Time)) map[string]any {
	var vals []float64
	for _, p := range pipelines {
		start, end := extract(p)
		if start.IsZero() || end.IsZero() {
			continue
		}
		d := end.Sub(start).Seconds()
		if d >= 0 {
			vals = append(vals, d)
		}
	}
	return statsMap(vals)
}

func computeFailures(pipelines []analyticsPipeline) map[string]any {
	blockTotal := map[string]int{}
	blockFail := map[string]int{}

	for _, p := range pipelines {
		for _, b := range p.Blocks {
			blockTotal[b.Name]++
			if b.Result == "failed" {
				blockFail[b.Name]++
			}
		}
	}

	type blockRate struct {
		Name   string `json:"name"`
		Total  int    `json:"total"`
		Failed int    `json:"failed"`
		Rate   string `json:"failure_rate"`
	}

	var rates []blockRate
	for name, total := range blockTotal {
		failed := blockFail[name]
		if failed > 0 {
			rate := float64(failed) / float64(total) * 100
			rates = append(rates, blockRate{
				Name:   name,
				Total:  total,
				Failed: failed,
				Rate:   fmt.Sprintf("%.0f%%", rate),
			})
		}
	}

	sort.Slice(rates, func(i, j int) bool {
		return rates[i].Failed > rates[j].Failed
	})

	passed, failed := 0, 0
	for _, p := range pipelines {
		switch p.Result {
		case "passed":
			passed++
		case "failed":
			failed++
		}
	}

	return map[string]any{
		"passed":         passed,
		"failed":         failed,
		"pass_rate":      fmtPct(passRate(pipelines)),
		"failing_blocks": rates,
	}
}

func computeFailureReasons(pipelines []analyticsPipeline) map[string]int {
	reasons := map[string]int{}
	for _, p := range pipelines {
		if p.Result == "failed" || p.Result == "stopped" {
			r := p.ResultReason
			if r == "" {
				r = "unknown"
			}
			reasons[r]++
		}
	}
	return reasons
}

func computeTriggers(pipelines []analyticsPipeline) map[string]int {
	triggers := map[string]int{}
	for _, p := range pipelines {
		t := p.TriggeredBy
		if t == "" {
			t = "unknown"
		}
		triggers[t]++
	}
	return triggers
}

func countDeploys(pipelines []analyticsPipeline) map[string]any {
	total := 0
	for _, p := range pipelines {
		if p.HasPromo {
			total++
		}
	}

	perDay := 0.0
	if analyticsDaysFlag > 0 {
		perDay = float64(total) / float64(analyticsDaysFlag)
	}

	return map[string]any{
		"total":    total,
		"per_day":  fmt.Sprintf("%.1f", perDay),
		"per_week": fmt.Sprintf("%.1f", perDay*7),
	}
}

// --- helpers ---

func passRate(pipelines []analyticsPipeline) float64 {
	passed, failed := 0, 0
	for _, p := range pipelines {
		switch p.Result {
		case "passed":
			passed++
		case "failed":
			failed++
		}
	}
	if passed+failed == 0 {
		return 0
	}
	return float64(passed) / float64(passed+failed) * 100
}

func statsMap(vals []float64) map[string]any {
	if len(vals) == 0 {
		return map[string]any{"avg": "n/a", "p50": "n/a", "p95": "n/a", "samples": 0}
	}
	sort.Float64s(vals)
	return map[string]any{
		"avg":     formatDuration(avg(vals)),
		"p50":     formatDuration(percentile(vals, 50)),
		"p95":     formatDuration(percentile(vals, 95)),
		"min":     formatDuration(vals[0]),
		"max":     formatDuration(vals[len(vals)-1]),
		"samples": len(vals),
	}
}

func avg(vals []float64) float64 {
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func formatDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", seconds)
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func triggerName(t int) string {
	switch t {
	case 0:
		return "hook"
	case 1:
		return "api"
	case 2:
		return "schedule"
	case 3:
		return "rerun"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

func fmtPct(v float64) string {
	return fmt.Sprintf("%.0f%%", v)
}

func parseTime(s string) time.Time {
	if s == "" || s == "1970-01-01 00:00:00.000000Z" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02 15:04:05.000000Z", s)
	if err != nil {
		t, _ = time.Parse("2006-01-02 15:04:05Z", s)
	}
	return t
}

// weekLabel returns "YYYY-WNN|Mon DD - Mon DD" with a sortable prefix.
func weekLabel(t time.Time) string {
	y, w := t.ISOWeek()
	jan1 := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	offset := int(time.Monday - jan1.Weekday())
	if offset > 0 {
		offset -= 7
	}
	monday := jan1.AddDate(0, 0, offset+(w-1)*7)
	sunday := monday.AddDate(0, 0, 6)
	return fmt.Sprintf("%d-W%02d|%s - %s", y, w, monday.Format("Jan 02"), sunday.Format("Jan 02"))
}

// weekSortKey extracts the "YYYY-WNN" prefix for chronological sorting.
func weekSortKey(label string) string {
	if idx := strings.Index(label, "|"); idx > 0 {
		return label[:idx]
	}
	return label
}

// weekDisplayLabel extracts the human-readable "Mon DD - Mon DD" part.
func weekDisplayLabel(label string) string {
	if idx := strings.Index(label, "|"); idx >= 0 && idx+1 < len(label) {
		return label[idx+1:]
	}
	return label
}

func init() {
	for _, cmd := range []*cobra.Command{analyticsSummaryCmd, analyticsDurationCmd, analyticsFailuresCmd, analyticsQueueCmd, analyticsDeploysCmd} {
		cmd.Flags().StringVar(&analyticsProjectFlag, "project", "", "project name (auto-detected from git if omitted)")
		cmd.Flags().StringVar(&analyticsBranchFlag, "branch", "", "filter by branch")
		cmd.Flags().IntVar(&analyticsDaysFlag, "days", 7, "time window in days")
		cmd.Flags().IntVar(&analyticsLimitFlag, "limit", 100, "max workflows to analyze")
	}

	analyticsTrendCmd.Flags().StringVar(&analyticsProjectFlag, "project", "", "project name (auto-detected from git if omitted)")
	analyticsTrendCmd.Flags().StringVar(&analyticsBranchFlag, "branch", "", "filter by branch")
	analyticsTrendCmd.Flags().IntVar(&analyticsWeeksFlag, "weeks", 4, "number of weeks to analyze")
	analyticsTrendCmd.Flags().IntVar(&analyticsLimitFlag, "limit", 200, "max workflows to analyze")

	analyticsCmd.AddCommand(analyticsSummaryCmd)
	analyticsCmd.AddCommand(analyticsDurationCmd)
	analyticsCmd.AddCommand(analyticsFailuresCmd)
	analyticsCmd.AddCommand(analyticsQueueCmd)
	analyticsCmd.AddCommand(analyticsDeploysCmd)
	analyticsCmd.AddCommand(analyticsTrendCmd)
	rootCmd.AddCommand(analyticsCmd)
}
