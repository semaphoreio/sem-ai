package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Project operations",
}

var projectListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all projects in the organization",
	Example: "  sem-agent project list\n  sem-agent project list --format table",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.List("projects")
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var projects []json.RawMessage
		if err := json.Unmarshal(resp.Body, &projects); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		// Extract summary for each project
		type projectSummary struct {
			Name        string `json:"name"`
			ID          string `json:"id"`
			RepoURL     string `json:"repo_url"`
			Visibility  string `json:"visibility"`
			Integration string `json:"integration_type"`
		}

		summaries := make([]projectSummary, 0, len(projects))
		for _, raw := range projects {
			var p struct {
				Metadata struct {
					Name string `json:"name"`
					ID   string `json:"id"`
				} `json:"metadata"`
				Spec struct {
					Visibility string `json:"visibility"`
					Repository struct {
						URL             string `json:"url"`
						IntegrationType string `json:"integration_type"`
					} `json:"repository"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			summaries = append(summaries, projectSummary{
				Name:        p.Metadata.Name,
				ID:          p.Metadata.ID,
				RepoURL:     p.Spec.Repository.URL,
				Visibility:  p.Spec.Visibility,
				Integration: p.Spec.Repository.IntegrationType,
			})
		}
		output.Result(summaries)
		return nil
	},
}

var projectShowCmd = &cobra.Command{
	Use:     "show <name>",
	Short:   "Show project details",
	Args:    cobra.ExactArgs(1),
	Example: "  sem-agent project show my-project\n  sem-agent project show my-project --format yaml",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()

		// Try direct GET by name
		resp, err := c.Get("projects", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		// If direct lookup fails, search by listing all projects
		if resp.StatusCode != 200 {
			listResp, err := c.List("projects")
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if listResp.StatusCode == 200 {
				var projects []json.RawMessage
				if err := json.Unmarshal(listResp.Body, &projects); err == nil {
					for _, raw := range projects {
						var p struct {
							Metadata struct {
								Name string `json:"name"`
							} `json:"metadata"`
						}
						if err := json.Unmarshal(raw, &p); err == nil && p.Metadata.Name == args[0] {
							var result any
							json.Unmarshal(raw, &result)
							output.Result(result)
							return nil
						}
					}
				}
			}
			output.Error("not_found", fmt.Sprintf("project %q not found", args[0]), 404)
			return fmt.Errorf("project not found")
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

var projectDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a project",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-agent project delete my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		c := client.New()
		resp, err := c.Delete("projects", args[0])
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
	projectUpdateVisibilityFlag string
	projectUpdateDescFlag       string
)

var projectUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update project settings",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent project update my-project --visibility public
  sem-agent project update my-project --description "My app"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		// Need to get current project to build PATCH body
		c := client.New()
		resp, err := c.Get("projects", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			// Try by listing
			projectID, err := resolveProjectID(args[0])
			if err != nil {
				output.Error("not_found", fmt.Sprintf("project %q not found", args[0]), 404)
				return err
			}
			// PATCH by ID
			spec := map[string]any{}
			if projectUpdateVisibilityFlag != "" {
				spec["visibility"] = projectUpdateVisibilityFlag
			}
			if projectUpdateDescFlag != "" {
				spec["description"] = projectUpdateDescFlag
			}
			patch := map[string]any{"spec": spec}
			bodyBytes, _ := json.Marshal(patch)
			patchResp, err := c.Patch("projects", projectID, bodyBytes)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if patchResp.StatusCode != 200 {
				output.Error("api_error", fmt.Sprintf("HTTP %d: %s", patchResp.StatusCode, string(patchResp.Body)), patchResp.StatusCode)
				return fmt.Errorf("API returned %d", patchResp.StatusCode)
			}
			var result any
			json.Unmarshal(patchResp.Body, &result)
			output.Result(result)
			return nil
		}

		// Unmarshal current project, apply changes, PATCH
		var proj map[string]any
		json.Unmarshal(resp.Body, &proj)

		spec, _ := proj["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		if projectUpdateVisibilityFlag != "" {
			spec["visibility"] = projectUpdateVisibilityFlag
		}
		if projectUpdateDescFlag != "" {
			spec["description"] = projectUpdateDescFlag
		}
		proj["spec"] = spec

		bodyBytes, _ := json.Marshal(proj)
		metadata, _ := proj["metadata"].(map[string]any)
		id := ""
		if metadata != nil {
			id, _ = metadata["id"].(string)
		}
		if id == "" {
			id = args[0]
		}

		patchResp, err := c.Patch("projects", id, bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if patchResp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", patchResp.StatusCode, string(patchResp.Body)), patchResp.StatusCode)
			return fmt.Errorf("API returned %d", patchResp.StatusCode)
		}
		var result any
		json.Unmarshal(patchResp.Body, &result)
		output.Result(result)
		return nil
	},
}

func init() {
	projectUpdateCmd.Flags().StringVar(&projectUpdateVisibilityFlag, "visibility", "", "project visibility: public or private")
	projectUpdateCmd.Flags().StringVar(&projectUpdateDescFlag, "description", "", "project description")

	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectShowCmd)
	projectCmd.AddCommand(projectUpdateCmd)
	projectCmd.AddCommand(projectDeleteCmd)
	rootCmd.AddCommand(projectCmd)
}
