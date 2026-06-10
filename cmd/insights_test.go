package cmd

import "testing"

func TestInsightsParams(t *testing.T) {
	v := insightsParams("ci.yml", "main", "2026-01-01", "2026-01-31", "range")
	if v.Get("pipeline_file") != "ci.yml" || v.Get("branch") != "main" {
		t.Errorf("base params wrong: %v", v)
	}
	if v.Get("from") != "2026-01-01" || v.Get("to") != "2026-01-31" || v.Get("aggregate") != "range" {
		t.Errorf("date/aggregate wrong: %v", v)
	}
}

func TestInsightsParams_omitsEmptyOptional(t *testing.T) {
	v := insightsParams("ci.yml", "", "", "", "")
	if v.Get("pipeline_file") != "ci.yml" {
		t.Error("pipeline_file must be present")
	}
	for _, k := range []string{"branch", "from", "to", "aggregate"} {
		if _, ok := v[k]; ok {
			t.Errorf("empty %q must be omitted", k)
		}
	}
}
