package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Job operations",
}

var jobShowCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show job details",
	Args:    cobra.ExactArgs(1),
	Example: "  sem-agent job show job-uuid-here",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("jobs", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}
		output.Result(result)
		return nil
	},
}

var jobLogCmd = &cobra.Command{
	Use:   "log <id>",
	Short: "Fetch job logs",
	Long:  "Fetches and displays job logs. Returns structured log events with timestamps, commands, output, and exit codes.",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent job log job-uuid-here
  sem-agent job log job-uuid-here --format json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("logs", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		// Parse log events
		var logs struct {
			Events []struct {
				Timestamp int64  `json:"timestamp"`
				Type      string `json:"event"`
				Output    string `json:"output"`
				Directive string `json:"directive"`
				ExitCode  int    `json:"exit_code"`
				JobResult string `json:"job_result"`
			} `json:"events"`
		}
		if err := json.Unmarshal(resp.Body, &logs); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		// For table format, flatten to readable output
		if formatFlag == "table" {
			var sb strings.Builder
			for _, e := range logs.Events {
				switch e.Type {
				case "cmd_started":
					sb.WriteString(fmt.Sprintf("$ %s\n", e.Directive))
				case "cmd_output":
					sb.WriteString(e.Output)
				case "cmd_finished":
					if e.ExitCode != 0 {
						sb.WriteString(fmt.Sprintf("\n[exit code: %d]\n", e.ExitCode))
					}
				case "job_finished":
					sb.WriteString(fmt.Sprintf("\n[job result: %s]\n", e.JobResult))
				}
			}
			fmt.Print(sb.String())
			return nil
		}

		// JSON/YAML: structured events
		type logEvent struct {
			Timestamp int64  `json:"timestamp"`
			Type      string `json:"type"`
			Output    string `json:"output,omitempty"`
			Command   string `json:"command,omitempty"`
			ExitCode  int    `json:"exit_code,omitempty"`
			JobResult string `json:"job_result,omitempty"`
		}

		events := make([]logEvent, 0, len(logs.Events))
		for _, e := range logs.Events {
			events = append(events, logEvent{
				Timestamp: e.Timestamp,
				Type:      e.Type,
				Output:    e.Output,
				Command:   e.Directive,
				ExitCode:  e.ExitCode,
				JobResult: e.JobResult,
			})
		}
		output.Result(events)
		return nil
	},
}

var jobListStatesFlag []string

var jobListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs by state",
	Example: `  sem-agent job list --states RUNNING,QUEUED
  sem-agent job list --states FINISHED`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		params := url.Values{}
		for _, s := range jobListStatesFlag {
			params.Add("states", s)
		}
		resp, err := c.ListWithParams("jobs", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		json.Unmarshal(resp.Body, &result)
		output.Result(result)
		return nil
	},
}

var jobStopCmd = &cobra.Command{
	Use:     "stop <id>",
	Short:   "Stop a running job",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent job stop job-uuid-here`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		path := fmt.Sprintf("jobs/%s/stop", args[0])
		resp, err := c.Post(path, nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "stopping", "job_id": args[0]})
		return nil
	},
}

func init() {
	jobListCmd.Flags().StringArrayVar(&jobListStatesFlag, "states", nil, "filter by state: RUNNING, QUEUED, FINISHED")
	jobCmd.AddCommand(jobListCmd)
	jobCmd.AddCommand(jobShowCmd)
	jobCmd.AddCommand(jobLogCmd)
	jobCmd.AddCommand(jobStopCmd)
	rootCmd.AddCommand(jobCmd)
}
