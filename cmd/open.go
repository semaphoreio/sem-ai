package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/gitutil"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

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

func openBrowser(url string) error {
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
	return cmd.Start()
}

func init() {
	openCmd.Flags().StringVar(&openProjectFlag, "project", "", "project name")
	openCmd.Flags().StringVar(&openWorkflowFlag, "workflow", "", "workflow ID to open directly")
	rootCmd.AddCommand(openCmd)
}
