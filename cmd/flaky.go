package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
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
func emitJSON(resp *client.Response) error {
	if resp.StatusCode != 200 {
		output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
		return fmt.Errorf("API returned %d", resp.StatusCode)
	}
	var result any
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		output.Error("parse_error", fmt.Sprintf("failed to decode response: %v", err), 1)
		return err
	}
	output.Result(result)
	return nil
}

// ---- flaky list ----

var (
	flakyListProject  string
	flakyListPage     int
	flakyListPageSize int
	flakyListSortF    string
	flakyListSortD    string
	flakyListFilters  flakyFilters
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
		return emitJSON(resp)
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

func init() {
	flakyListCmd.Flags().StringVar(&flakyListProject, "project", "", "project name or ID (required)")
	flakyListCmd.Flags().IntVar(&flakyListPage, "page", 1, "page number")
	flakyListCmd.Flags().IntVar(&flakyListPageSize, "page-size", 20, "results per page")
	flakyListCmd.Flags().StringVar(&flakyListSortF, "sort-field", "", "sort field (e.g. total_disruptions_count, pass_rate)")
	flakyListCmd.Flags().StringVar(&flakyListSortD, "sort-dir", "", "sort direction (asc|desc)")
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

	flakyCmd.AddCommand(flakyListCmd)
	flakyCmd.AddCommand(flakyShowCmd)
	flakyCmd.AddCommand(flakyDisruptionsCmd)
	flakyCmd.AddCommand(flakyTrendsCmd)
	rootCmd.AddCommand(flakyCmd)
}
