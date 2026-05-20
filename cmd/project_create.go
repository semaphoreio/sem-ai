package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/gitutil"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

const (
	githubIntegrationToken = "github_token"
	githubIntegrationApp   = "github_app"
)

const defaultPipelineYAML = `version: v1.0
name: Initial Pipeline
agent:
  machine:
    type: e1-standard-2
    os_image: ubuntu2004
blocks:
  - name: Build
    task:
      jobs:
        - name: Hello
          commands:
            - checkout
            - echo "hello, semaphore"
`

var (
	projectCreateRepoURLFlag     string
	projectCreateNameFlag        string
	projectCreateIntegrationFlag string
	projectCreateRemoteFlag      string
	projectCreateSkipYAMLFlag    bool
	projectCreateFailOnExists    bool
)

var projectCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a project from the current git repository",
	Example: `  sem-ai project create
  sem-ai project create --repo-url git@github.com:org/repo.git
  sem-ai project create --name my-project --github-integration github_app
  sem-ai project create --skip-yaml
  sem-ai project create --fail-on-exists`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}

		integration := projectCreateIntegrationFlag
		if integration != githubIntegrationToken && integration != githubIntegrationApp {
			output.Error("invalid_argument",
				fmt.Sprintf("invalid --github-integration %q (want %q or %q)",
					integration, githubIntegrationToken, githubIntegrationApp), 2)
			return fmt.Errorf("invalid integration type")
		}

		repoURL := projectCreateRepoURLFlag
		if repoURL == "" {
			remote := projectCreateRemoteFlag
			if remote == "" {
				remote = "origin"
			}
			detected, err := gitutil.RemoteURL(remote)
			if err != nil {
				output.Error("missing_repo_url",
					fmt.Sprintf("no --repo-url and no git remote %q in cwd", remote), 2)
				return err
			}
			repoURL = detected
		}

		name := projectCreateNameFlag
		if name == "" {
			derived, err := projectNameFromURL(repoURL)
			if err != nil {
				output.Error("invalid_repo_url", err.Error(), 2)
				return err
			}
			name = derived
		}

		c := client.New()

		if existing, found, err := lookupProject(c, name); err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		} else if found {
			if projectCreateFailOnExists {
				output.Error("already_exists",
					fmt.Sprintf("project %q already exists in this organization", name),
					409)
				return fmt.Errorf("project already exists")
			}
			output.Result(map[string]any{
				"status":         "exists",
				"project":        existing,
				"yaml_generated": false,
				"yaml_path":      "",
			})
			return nil
		}

		body := map[string]any{
			"apiVersion": "v1alpha",
			"kind":       "Project",
			"metadata":   map[string]any{"name": name},
			"spec": map[string]any{
				"repository": map[string]any{
					"url":              repoURL,
					"run_on":           []string{"branches", "tags"},
					"integration_type": integration,
				},
			},
		}
		payload, _ := json.Marshal(body)

		resp, err := c.Post("projects", payload)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error",
				fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)),
				resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var project any
		if err := json.Unmarshal(resp.Body, &project); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		yamlGenerated := false
		yamlPath := ""
		if !projectCreateSkipYAMLFlag {
			generated, path, err := writeDefaultPipelineYAML()
			if err != nil {
				output.Error("yaml_write_error", err.Error(), 1)
				return err
			}
			yamlGenerated = generated
			yamlPath = path
		}

		output.Result(map[string]any{
			"status":         "created",
			"project":        project,
			"yaml_generated": yamlGenerated,
			"yaml_path":      yamlPath,
		})
		return nil
	},
}

var projectNameRegex = regexp.MustCompile(`.+[:/]([^/]+)/([^/]+?)(?:\.git)?$`)

// lookupProject returns the existing project (if any) for the given name.
// Tries direct GET first; falls back to listing on non-200 since some servers
// only allow lookup by ID via direct path.
func lookupProject(c *client.Client, name string) (any, bool, error) {
	resp, err := c.Get("projects", name)
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == 200 {
		var project any
		if err := json.Unmarshal(resp.Body, &project); err != nil {
			return nil, false, err
		}
		return project, true, nil
	}
	if resp.StatusCode != 404 {
		// Fall through to list-based search on unexpected non-200 (e.g. 400)
		// — older servers reject name lookups on the GET-by-id path.
		listResp, err := c.List("projects")
		if err != nil {
			return nil, false, err
		}
		if listResp.StatusCode != 200 {
			return nil, false, fmt.Errorf("HTTP %d on project lookup: %s",
				listResp.StatusCode, string(listResp.Body))
		}
		var projects []json.RawMessage
		if err := json.Unmarshal(listResp.Body, &projects); err != nil {
			return nil, false, err
		}
		for _, raw := range projects {
			var p struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(raw, &p); err == nil && p.Metadata.Name == name {
				var project any
				_ = json.Unmarshal(raw, &project)
				return project, true, nil
			}
		}
	}
	return nil, false, nil
}

func projectNameFromURL(repoURL string) (string, error) {
	m := projectNameRegex.FindStringSubmatch(repoURL)
	if len(m) < 3 {
		return "", fmt.Errorf("unsupported git remote format %q; expected git@HOST:owner/repo[.git] or https://HOST/owner/repo[.git]", repoURL)
	}
	return m[2], nil
}

func writeDefaultPipelineYAML() (bool, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(filepath.Join(cwd, ".git")); os.IsNotExist(err) {
		return false, "", nil
	}
	dir := filepath.Join(cwd, ".semaphore")
	path := filepath.Join(dir, "semaphore.yml")
	if _, err := os.Stat(path); err == nil {
		return false, path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, "", err
	}
	if err := os.WriteFile(path, []byte(defaultPipelineYAML), 0o644); err != nil {
		return false, "", err
	}
	return true, path, nil
}

func init() {
	projectCreateCmd.Flags().StringVar(&projectCreateRepoURLFlag, "repo-url", "",
		"git repository URL (default: origin of cwd)")
	projectCreateCmd.Flags().StringVar(&projectCreateNameFlag, "name", "",
		"project name (default: derived from repo URL)")
	projectCreateCmd.Flags().StringVar(&projectCreateIntegrationFlag, "github-integration",
		githubIntegrationToken,
		fmt.Sprintf("GitHub integration: %q or %q", githubIntegrationToken, githubIntegrationApp))
	projectCreateCmd.Flags().StringVar(&projectCreateRemoteFlag, "remote", "origin",
		"git remote to detect (when --repo-url not set)")
	projectCreateCmd.Flags().BoolVar(&projectCreateSkipYAMLFlag, "skip-yaml", false,
		"don't generate .semaphore/semaphore.yml in cwd")
	projectCreateCmd.Flags().BoolVar(&projectCreateFailOnExists, "fail-on-exists", false,
		"exit non-zero if a project with the same name already exists (default: return existing)")

	projectCmd.AddCommand(projectCreateCmd)
}
