package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/gitutil"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

// openBrowserExitWait bounds how long openBrowser waits for the opener
// process (open/xdg-open/rundll32) to exit before deciding it launched
// successfully. This only measures the opener, not the browser it hands off
// to — openers normally exit almost instantly — but a nonzero exit within
// this window (no URL handler registered, no display, broken install) is a
// reliable "browser unavailable" signal that a bare cmd.Start() misses.
// It's a var (not a const) so tests can shorten it instead of waiting.
var openBrowserExitWait = 3 * time.Second

var (
	openProjectFlag  string
	openWorkflowFlag string
)

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open workflow/pipeline in browser",
	Long:  "Opens the Semaphore UI for the current branch's latest workflow.",
	Example: `  sem-ai open
  sem-ai open --workflow abc123
  sem-ai open --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}

		host := config.GetHost()

		// If workflow ID given, open directly
		if openWorkflowFlag != "" {
			url := fmt.Sprintf("https://%s/workflows/%s", host, openWorkflowFlag)
			return openBrowser(url)
		}

		// Otherwise, detect project and find latest workflow
		project := openProjectFlag
		if project == "" {
			p, err := detectProject()
			if err != nil {
				output.Error("context_error", "could not detect project — use --project", 1)
				return err
			}
			project = p
		}

		projectID, err := resolveProjectID(project)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		// Get current branch
		branch, _ := gitutil.CurrentBranch()

		params := url.Values{}
		params.Set("project_id", projectID)
		if branch != "" {
			params.Set("branch_name", branch)
		}

		c := client.New()
		resp, err := c.ListWithParams("plumber-workflows", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var workflows []struct {
			WfID string `json:"wf_id"`
		}
		if resp.StatusCode == 200 {
			_ = json.Unmarshal(resp.Body, &workflows)
		}

		if len(workflows) > 0 {
			wfURL := fmt.Sprintf("https://%s/workflows/%s", host, workflows[0].WfID)
			output.Result(map[string]string{
				"url":     wfURL,
				"project": project,
			})
			return openBrowser(wfURL)
		}

		// Fallback: open project page
		projectURL := fmt.Sprintf("https://%s/projects/%s", host, project)
		output.Result(map[string]string{
			"url":     projectURL,
			"project": project,
		})
		return openBrowser(projectURL)
	},
}

// openBrowser is a var (not a plain func) so tests can stub it out — it
// otherwise has the side effect of actually launching a browser.
//
// cmd.Start() only proves the opener process was spawned, not that a browser
// actually came up: on a box with a broken or headless opener, Start() still
// returns nil. To catch fast, synchronous failures (no handler registered,
// no display, broken install), we give the opener a short window to exit and
// treat a nonzero exit within it as failure. If it's still running past the
// window we assume it launched normally — most openers hand off to the
// browser and exit almost instantly, so this adds no perceptible latency on
// the working desktop path.
var openBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(openBrowserExitWait):
		return nil
	}
}

func init() {
	openCmd.Flags().StringVar(&openProjectFlag, "project", "", "project name")
	openCmd.Flags().StringVar(&openWorkflowFlag, "workflow", "", "workflow ID to open directly")
	rootCmd.AddCommand(openCmd)
}
