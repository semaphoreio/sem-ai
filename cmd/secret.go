package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Secret management — org and project level",
}

var secretProjectFlag string

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secrets (org-level, or --project for project-level)",
	Example: `  sem-ai secret list
  sem-ai secret list --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		var resp *client.Response
		var err error

		if secretProjectFlag != "" {
			projectID, err := resolveProjectID(secretProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			// Project secrets: GET /v1/projects/{id}/secrets
			resp, err = c.ListVersioned("v1", fmt.Sprintf("projects/%s/secrets", projectID))
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		} else {
			// Org secrets: GET /v1beta/secrets
			resp, err = c.ListVersioned("v1beta", "secrets")
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
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

var secretShowProjectFlag string

var secretShowCmd = &cobra.Command{
	Use:     "show <name>",
	Short:   "Show secret details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai secret show my-secret
  sem-ai secret show my-secret --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		var resp *client.Response
		var err error

		if secretShowProjectFlag != "" {
			projectID, err := resolveProjectID(secretShowProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			resp, err = c.GetVersioned("v1", fmt.Sprintf("projects/%s/secrets", projectID), args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		} else {
			resp, err = c.GetVersioned("v1beta", "secrets", args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
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

var (
	secretCreateProjectFlag string
	secretCreateEnvFlag     []string
)

var secretCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a secret",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai secret create my-secret --env KEY=VALUE --env DB_URL=postgres://...
  sem-ai secret create my-secret --project my-project --env API_KEY=abc123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}

		name := args[0]

		// Build env vars
		envVars := make([]map[string]string, 0)
		for _, e := range secretCreateEnvFlag {
			for i := range e {
				if e[i] == '=' {
					envVars = append(envVars, map[string]string{"name": e[:i], "value": e[i+1:]})
					break
				}
			}
		}

		files := make([]map[string]string, 0)

		secret := map[string]any{
			"apiVersion": "v1beta",
			"kind":       "Secret",
			"metadata":   map[string]string{"name": name},
			"data": map[string]any{
				"env_vars": envVars,
				"files":    files,
			},
		}
		body, _ := json.Marshal(secret)

		c := client.New()
		var resp *client.Response
		var err error

		if secretCreateProjectFlag != "" {
			projectID, err := resolveProjectID(secretCreateProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			resp, err = c.PostVersioned("v1", fmt.Sprintf("projects/%s/secrets", projectID), body)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		} else {
			resp, err = c.PostVersioned("v1beta", "secrets", body)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
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

var (
	secretUpdateProjectFlag string
	secretUpdateEnvFlag     []string
)

var secretUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a secret (replaces env vars)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai secret update my-secret --env KEY=NEW_VALUE
  sem-ai secret update my-secret --project my-project --env DB_URL=new-url`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		name := args[0]
		envVars := make([]map[string]string, 0)
		for _, e := range secretUpdateEnvFlag {
			for i := range e {
				if e[i] == '=' {
					envVars = append(envVars, map[string]string{"name": e[:i], "value": e[i+1:]})
					break
				}
			}
		}
		secret := map[string]any{
			"apiVersion": "v1beta",
			"kind":       "Secret",
			"metadata":   map[string]string{"name": name},
			"data": map[string]any{
				"env_vars": envVars,
				"files":    make([]any, 0),
			},
		}
		body, _ := json.Marshal(secret)
		c := client.New()
		var resp *client.Response
		var err error
		if secretUpdateProjectFlag != "" {
			projectID, err := resolveProjectID(secretUpdateProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			resp, err = c.PatchVersioned("v1", fmt.Sprintf("projects/%s/secrets", projectID), name, body)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		} else {
			resp, err = c.PatchVersioned("v1beta", "secrets", name, body)
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
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

var secretDeleteProjectFlag string

var secretDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a secret",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai secret delete my-secret
  sem-ai secret delete my-secret --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		var resp *client.Response
		var err error

		if secretDeleteProjectFlag != "" {
			projectID, err := resolveProjectID(secretDeleteProjectFlag)
			if err != nil {
				output.Error("project_error", err.Error(), 1)
				return err
			}
			resp, err = c.DeleteVersioned("v1", fmt.Sprintf("projects/%s/secrets", projectID), args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		} else {
			resp, err = c.DeleteVersioned("v1beta", "secrets", args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
		}

		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		output.Result(map[string]string{"status": "deleted", "name": args[0]})
		return nil
	},
}

func init() {
	secretListCmd.Flags().StringVar(&secretProjectFlag, "project", "", "project name for project-level secrets")
	secretShowCmd.Flags().StringVar(&secretShowProjectFlag, "project", "", "project name for project-level secrets")
	secretCreateCmd.Flags().StringVar(&secretCreateProjectFlag, "project", "", "project name for project-level secrets")
	secretCreateCmd.Flags().StringArrayVar(&secretCreateEnvFlag, "env", nil, "environment variable as KEY=VALUE")
	secretDeleteCmd.Flags().StringVar(&secretDeleteProjectFlag, "project", "", "project name for project-level secrets")

	secretUpdateCmd.Flags().StringVar(&secretUpdateProjectFlag, "project", "", "project name for project-level secrets")
	secretUpdateCmd.Flags().StringArrayVar(&secretUpdateEnvFlag, "env", nil, "environment variable as KEY=VALUE")

	secretCmd.AddCommand(secretListCmd)
	secretCmd.AddCommand(secretShowCmd)
	secretCmd.AddCommand(secretCreateCmd)
	secretCmd.AddCommand(secretUpdateCmd)
	secretCmd.AddCommand(secretDeleteCmd)
	rootCmd.AddCommand(secretCmd)
}
