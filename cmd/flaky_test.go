package cmd

import (
	"testing"
)

func TestFlakyFilterParams_mapsAllowlist(t *testing.T) {
	f := flakyFilters{
		Branch: "main", TestName: "Login Test", Label: "flaky,slow",
		Resolved: "false", PassRate: ">=80", DateFrom: "2026-01-01",
	}
	got := f.toValues()

	checks := map[string]string{
		"branch":    "main",
		"test_name": "Login Test",
		"label":     "flaky,slow",
		"resolved":  "false",
		"pass_rate": ">=80",
		"date_from": "2026-01-01",
	}
	for k, want := range checks {
		if got.Get(k) != want {
			t.Errorf("param %q = %q, want %q", k, got.Get(k), want)
		}
	}
}

func TestFlakyFilterParams_omitsEmpty(t *testing.T) {
	got := flakyFilters{Branch: "main"}.toValues()
	if _, ok := got["test_name"]; ok {
		t.Error("empty test_name must not be sent")
	}
	if len(got) != 1 {
		t.Errorf("only non-empty filters sent, got %v", got)
	}
}

func TestFlakyListParams_addsPagingAndSort(t *testing.T) {
	got := flakyListParams(flakyFilters{Branch: "main"}, 2, 50, "pass_rate", "asc")
	if got.Get("page") != "2" || got.Get("page_size") != "50" {
		t.Errorf("paging not set: %v", got)
	}
	if got.Get("sort_field") != "pass_rate" || got.Get("sort_dir") != "asc" {
		t.Errorf("sort not set: %v", got)
	}
	if got.Get("branch") != "main" {
		t.Error("filters must be merged into list params")
	}
}

func TestFlakyResourcePath(t *testing.T) {
	got := flakyResourcePath("proj-1", "test-9", "disruptions")
	want := "projects/proj-1/test_results/flaky_tests/test-9/disruptions"
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if base := flakyResourcePath("proj-1", "test-9", ""); base != "projects/proj-1/test_results/flaky_tests/test-9" {
		t.Errorf("base path wrong: %q", base)
	}
}

func TestFlakyDisruptionsParams(t *testing.T) {
	v := pagedFilterParams(flakyFilters{Branch: "main"}, 3, 25)
	if v.Get("page") != "3" || v.Get("page_size") != "25" || v.Get("branch") != "main" {
		t.Errorf("disruptions params wrong: %v", v)
	}
}

func TestFlakyTrendsEndpoint(t *testing.T) {
	cases := map[string]string{
		"flaky":       "projects/p/test_results/flaky_history",
		"disruptions": "projects/p/test_results/disruption_history",
		"":            "projects/p/test_results/flaky_history",
	}
	for metric, want := range cases {
		if got, _ := flakyTrendsPath("p", metric); got != want {
			t.Errorf("metric %q -> %q, want %q", metric, got, want)
		}
	}
	if _, err := flakyTrendsPath("p", "bogus"); err == nil {
		t.Error("invalid metric must error")
	}
}

func TestClampPageSize(t *testing.T) {
	cases := map[int]int{100000: 100, 101: 100, 100: 100, 50: 50, 1: 1, 0: 1, -5: 1}
	for in, want := range cases {
		if got := clampPageSize(in); got != want {
			t.Errorf("clampPageSize(%d) = %d, want %d", in, got, want)
		}
	}
	// and it flows through the param builders
	if v := flakyListParams(flakyFilters{}, 1, 100000, "", ""); v.Get("page_size") != "100" {
		t.Errorf("flakyListParams page_size not clamped: %q", v.Get("page_size"))
	}
	if v := pagedFilterParams(flakyFilters{}, 1, 100000); v.Get("page_size") != "100" {
		t.Errorf("pagedFilterParams page_size not clamped: %q", v.Get("page_size"))
	}
}
