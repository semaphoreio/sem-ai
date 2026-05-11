package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Dashboard management",
}

var dashboardListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List dashboards",
	Example: `  sem-agent dashboard list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.List("dashboards")
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

var dashboardShowCmd = &cobra.Command{
	Use:     "show <name>",
	Short:   "Show dashboard details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent dashboard show my-dashboard`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Get("dashboards", args[0])
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

var dashboardDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a dashboard",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent dashboard delete my-dashboard`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Delete("dashboards", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "name": args[0]})
		return nil
	},
}

var (
	dashCreateWidgetsFlag []string
)

var dashboardCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a dashboard",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent dashboard create my-dashboard
  sem-agent dashboard create my-dashboard --widget "project=my-app,branch=main"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		widgets := make([]map[string]any, 0)
		for _, w := range dashCreateWidgetsFlag {
			widget := map[string]any{}
			// Parse "key=val,key=val" format
			for _, pair := range splitComma(w) {
				for i := range pair {
					if pair[i] == '=' {
						widget[pair[:i]] = pair[i+1:]
						break
					}
				}
			}
			if len(widget) > 0 {
				widgets = append(widgets, widget)
			}
		}

		body := map[string]any{
			"apiVersion": "v1alpha",
			"kind":       "Dashboard",
			"metadata": map[string]string{
				"name": args[0],
			},
			"spec": map[string]any{
				"widgets": widgets,
			},
		}
		bodyBytes, _ := json.Marshal(body)

		c := client.New()
		resp, err := c.Post("dashboards", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Result(map[string]string{"status": "created", "name": args[0]})
			return nil
		}
		output.Result(result)
		return nil
	},
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := range s {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func init() {
	dashboardCreateCmd.Flags().StringArrayVar(&dashCreateWidgetsFlag, "widget", nil, "widget definition as key=val,key=val")

	dashboardCmd.AddCommand(dashboardListCmd)
	dashboardCmd.AddCommand(dashboardShowCmd)
	dashboardCmd.AddCommand(dashboardCreateCmd)
	dashboardCmd.AddCommand(dashboardDeleteCmd)
	rootCmd.AddCommand(dashboardCmd)
}
