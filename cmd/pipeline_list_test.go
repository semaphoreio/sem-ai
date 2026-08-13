package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/output"
)

// setPipelineListFlags sets the pipeline-list flag vars and restores them
// (RunE is invoked directly, bypassing cobra's flag parsing).
func setPipelineListFlags(t *testing.T, project string, days, limit int, full bool) {
	t.Helper()
	prevProject, prevBranch := pipelineListProjectFlag, pipelineListBranchFlag
	prevDays, prevLimit, prevFull := pipelineListDaysFlag, pipelineListLimitFlag, pipelineListFullFlag
	t.Cleanup(func() {
		pipelineListProjectFlag, pipelineListBranchFlag = prevProject, prevBranch
		pipelineListDaysFlag, pipelineListLimitFlag, pipelineListFullFlag = prevDays, prevLimit, prevFull
	})
	pipelineListProjectFlag, pipelineListBranchFlag = project, ""
	pipelineListDaysFlag, pipelineListLimitFlag, pipelineListFullFlag = days, limit, full
}

func pipelineListHandler(pages map[string]string, lastPage int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/projects/"):
			fmt.Fprint(w, `{"metadata":{"id":"proj-uuid","name":"proj-x"}}`)
		case strings.Contains(r.URL.Path, "/pipelines"):
			page := r.URL.Query().Get("page")
			if page == "" {
				page = "1"
			}
			body, ok := pages[page]
			if !ok {
				body = "[]"
			}
			if ok && page != fmt.Sprintf("%d", lastPage) {
				w.Header().Set("Link", `<next>; rel="next"`)
			}
			fmt.Fprint(w, body)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestPipelineListSendsDefaultWindow(t *testing.T) {
	reqs, stdout, _ := apiMock(t, pipelineListHandler(map[string]string{"1": "[]"}, 1))
	setPipelineListFlags(t, "proj-x", 30, 30, false)

	if err := pipelineListCmd.RunE(pipelineListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var listReq *capturedReq
	for i := range *reqs {
		if strings.Contains((*reqs)[i].Path, "/pipelines") {
			listReq = &(*reqs)[i]
		}
	}
	if listReq == nil {
		t.Fatal("no pipelines request made")
	}
	if listReq.Query.Get("created_after") == "" {
		t.Error("default list must send created_after")
	}
	if listReq.Query.Get("page_size") != "30" {
		t.Errorf("default list must send page_size=30, got %q", listReq.Query.Get("page_size"))
	}
	if !strings.Contains(stdout.String(), "[]") {
		t.Errorf("empty list must print [], got %q", stdout.String())
	}
}

func TestPipelineListFullPaginates(t *testing.T) {
	pages := map[string]string{
		"1": `[{"ppl_id":"a"},{"ppl_id":"b"}]`,
		"2": `[{"ppl_id":"c"}]`,
	}
	reqs, stdout, _ := apiMock(t, pipelineListHandler(pages, 2))
	setPipelineListFlags(t, "proj-x", 30, 30, true)

	if err := pipelineListCmd.RunE(pipelineListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var listReqs []capturedReq
	for _, r := range *reqs {
		if strings.Contains(r.Path, "/pipelines") {
			listReqs = append(listReqs, r)
		}
	}
	if len(listReqs) != 2 {
		t.Fatalf("--full must follow pagination, got %d pipelines requests", len(listReqs))
	}
	for _, r := range listReqs {
		if r.Query.Get("created_after") != "" || r.Query.Get("page_size") != "" {
			t.Errorf("--full with default flags must not send window params, got %v", r.Query)
		}
	}

	var items []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("output not a JSON array: %v (%q)", err, stdout.String())
	}
	if len(items) != 3 {
		t.Fatalf("--full must return all pages' items, got %d", len(items))
	}
	if _, ok := items[0]["ppl_id"]; !ok {
		t.Error("--full must pass raw items through untouched (ppl_id key missing)")
	}
}

func TestPipelineListRejectsNegativeFlags(t *testing.T) {
	reqs, _, _ := apiMock(t, pipelineListHandler(nil, 0))
	setPipelineListFlags(t, "proj-x", -1, 30, false)

	if err := pipelineListCmd.RunE(pipelineListCmd, nil); err == nil {
		t.Fatal("negative --days must be rejected")
	}
	setPipelineListFlags(t, "proj-x", 30, -1, false)
	if err := pipelineListCmd.RunE(pipelineListCmd, nil); err == nil {
		t.Fatal("negative --limit must be rejected")
	}
	if len(*reqs) != 0 {
		t.Errorf("validation must fail before any API request, got %d requests", len(*reqs))
	}
}

func TestPipelineListWarnsOnSkippedItems(t *testing.T) {
	pages := map[string]string{"1": `[{"ppl_id":"a"},{"ppl_id":"b","created_at":"not-a-proto-timestamp"}]`}
	_, _, stderr := apiMock(t, pipelineListHandler(pages, 1))
	setPipelineListFlags(t, "proj-x", 30, 30, false)

	if err := pipelineListCmd.RunE(pipelineListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(stderr.String(), "skipped 1 unparseable") {
		t.Errorf("dropped rows must produce a warning, stderr: %q", stderr.String())
	}
}

func TestMCPCompactOverride(t *testing.T) {
	prevSource, prevFormat := invocationSource, formatFlag
	t.Cleanup(func() {
		invocationSource, formatFlag = prevSource, prevFormat
		output.SetFormat("json")
	})

	invocationSource, formatFlag = "semai-mcp", "json"
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}
	if got := output.GetFormat(); got != "compact" {
		t.Errorf("MCP surface must default to compact, got %q", got)
	}

	invocationSource = "semai-cli"
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}
	if got := output.GetFormat(); got != "json" {
		t.Errorf("CLI surface must stay pretty json, got %q", got)
	}
}
