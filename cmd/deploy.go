package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deployment target operations: visibility and management",
}

var deployTargetsProjectFlag string

var deployTargetsCmd = &cobra.Command{
	Use:     "targets",
	Short:   "List deployment targets for a project",
	Example: `  sem-ai deploy targets --project my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(deployTargetsProjectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		params := url.Values{}
		params.Set("project_id", projectID)
		resp, err := c.ListWithParams("deployment_targets", params)
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

var deployShowCmd = &cobra.Command{
	Use:     "show <target-id>",
	Short:   "Show deployment target details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai deploy show <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Get("deployment_targets", args[0])
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

var deployHistoryCmd = &cobra.Command{
	Use:     "history <target-id>",
	Short:   "Show deployment history for a target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai deploy history <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Get("deployment_targets", args[0]+"/history")
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

var deployActivateCmd = &cobra.Command{
	Use:     "activate <target-id>",
	Short:   "Activate a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai deploy activate <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Patch("deployment_targets", args[0]+"/activate", nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "activated", "target_id": args[0]})
		return nil
	},
}

var deployDeactivateCmd = &cobra.Command{
	Use:     "deactivate <target-id>",
	Short:   "Deactivate a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai deploy deactivate <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Patch("deployment_targets", args[0]+"/deactivate", nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deactivated", "target_id": args[0]})
		return nil
	},
}

var deployDeleteCmd = &cobra.Command{
	Use:     "delete <target-id>",
	Short:   "Delete a deployment target",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai deploy delete <target-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		c := client.New()
		token := client.NewRequestToken()
		resp, err := c.DeleteWithParams("deployment_targets", args[0], url.Values{"unique_token": {token}})
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "target_id": args[0]})
		return nil
	},
}

// deployTargetSpec holds the flag values for create + update.
type deployTargetSpec struct {
	projectFlag string
	description string
	url         string

	tagRegex         string
	tagExact         string
	branchRegex      string
	branchExact      string
	allowAllBranches bool
	allowAllTags     bool
	allowPRs         bool

	subjectAny   bool
	subjectUsers []string
	subjectRoles []string
	subjectAuto  bool

	envVars []string
	files   []string

	bookmark1 string
	bookmark2 string
	bookmark3 string

	uniqueToken string
}

var uuidRegexp = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// toBody builds the JSON request body. Empty fields are omitted so that PATCH
// callers can pass a partial update without nuking other fields.
func (s *deployTargetSpec) toBody(name string) (map[string]any, error) {
	body := map[string]any{}
	if name != "" {
		body["name"] = name
	}
	if s.description != "" {
		body["description"] = s.description
	}
	if s.url != "" {
		body["url"] = s.url
	}
	if s.bookmark1 != "" {
		body["bookmark_parameter1"] = s.bookmark1
	}
	if s.bookmark2 != "" {
		body["bookmark_parameter2"] = s.bookmark2
	}
	if s.bookmark3 != "" {
		body["bookmark_parameter3"] = s.bookmark3
	}

	objRules := []map[string]any{}
	if s.allowAllBranches {
		objRules = append(objRules, map[string]any{"type": "BRANCH", "match_mode": "ALL", "pattern": ""})
	}
	if s.branchExact != "" {
		objRules = append(objRules, map[string]any{"type": "BRANCH", "match_mode": "EXACT", "pattern": s.branchExact})
	}
	if s.branchRegex != "" {
		objRules = append(objRules, map[string]any{"type": "BRANCH", "match_mode": "REGEX", "pattern": s.branchRegex})
	}
	if s.allowAllTags {
		objRules = append(objRules, map[string]any{"type": "TAG", "match_mode": "ALL", "pattern": ""})
	}
	if s.tagExact != "" {
		objRules = append(objRules, map[string]any{"type": "TAG", "match_mode": "EXACT", "pattern": s.tagExact})
	}
	if s.tagRegex != "" {
		objRules = append(objRules, map[string]any{"type": "TAG", "match_mode": "REGEX", "pattern": s.tagRegex})
	}
	if s.allowPRs {
		objRules = append(objRules, map[string]any{"type": "PR", "match_mode": "ALL", "pattern": ""})
	}
	if len(objRules) > 0 {
		body["object_rules"] = objRules
	}

	subjRules := []map[string]any{}
	if s.subjectAny {
		subjRules = append(subjRules, map[string]any{"type": "ANY", "subject_id": ""})
	}
	for _, u := range s.subjectUsers {
		entry := map[string]any{"type": "USER"}
		if uuidRegexp.MatchString(u) {
			entry["subject_id"] = u
		} else {
			entry["git_login"] = u
		}
		subjRules = append(subjRules, entry)
	}
	for _, r := range s.subjectRoles {
		subjRules = append(subjRules, map[string]any{"type": "ROLE", "subject_id": r})
	}
	if s.subjectAuto {
		subjRules = append(subjRules, map[string]any{"type": "AUTO", "subject_id": ""})
	}
	if len(subjRules) > 0 {
		body["subject_rules"] = subjRules
	}

	envVars := []map[string]any{}
	for _, kv := range s.envVars {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--env-var must be NAME=VALUE, got %q", kv)
		}
		envVars = append(envVars, map[string]any{"name": name, "value": value})
	}
	if len(envVars) > 0 {
		body["env_vars"] = envVars
	}

	files := []map[string]any{}
	for _, pf := range s.files {
		path, local, ok := strings.Cut(pf, "=")
		if !ok {
			return nil, fmt.Errorf("--file must be PATH=LOCAL_SOURCE, got %q", pf)
		}
		content, err := os.ReadFile(local)
		if err != nil {
			return nil, fmt.Errorf("--file %s: %w", local, err)
		}
		files = append(files, map[string]any{"path": path, "content": base64.StdEncoding.EncodeToString(content)})
	}
	if len(files) > 0 {
		body["files"] = files
	}

	return body, nil
}

func bindDeploySpecFlags(cmd *cobra.Command, s *deployTargetSpec) {
	cmd.Flags().StringVar(&s.projectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")
	cmd.Flags().StringVar(&s.description, "description", "", "target description")
	cmd.Flags().StringVar(&s.url, "url", "", "target URL")

	cmd.Flags().StringVar(&s.tagRegex, "tag-regex", "", "allow tags matching regex (e.g. '^v[0-9].*')")
	cmd.Flags().StringVar(&s.tagExact, "tag-exact", "", "allow exact tag name")
	cmd.Flags().StringVar(&s.branchRegex, "branch-regex", "", "allow branches matching regex")
	cmd.Flags().StringVar(&s.branchExact, "branch-exact", "", "allow exact branch name")
	cmd.Flags().BoolVar(&s.allowAllBranches, "allow-all-branches", false, "allow promotion from any branch")
	cmd.Flags().BoolVar(&s.allowAllTags, "allow-all-tags", false, "allow promotion from any tag")
	cmd.Flags().BoolVar(&s.allowPRs, "allow-prs", false, "allow promotion from pull requests")

	cmd.Flags().BoolVar(&s.subjectAny, "subject-any", false, "allow anyone to trigger the promotion")
	cmd.Flags().StringArrayVar(&s.subjectUsers, "subject-user", nil, "allow specific user (UUID or git_login); repeatable")
	cmd.Flags().StringArrayVar(&s.subjectRoles, "subject-role", nil, "allow members of role (e.g. Admin, Contributor); repeatable")
	cmd.Flags().BoolVar(&s.subjectAuto, "subject-auto", false, "allow auto-promotion conditions to trigger the promotion")

	cmd.Flags().StringArrayVar(&s.envVars, "env-var", nil, "target-bound env var NAME=VALUE; repeatable")
	cmd.Flags().StringArrayVar(&s.files, "file", nil, "target-bound file PATH=LOCAL_SOURCE (base64-encoded); repeatable")

	cmd.Flags().StringVar(&s.bookmark1, "bookmark1", "", "deployment history filter (bookmark_parameter1)")
	cmd.Flags().StringVar(&s.bookmark2, "bookmark2", "", "deployment history filter (bookmark_parameter2)")
	cmd.Flags().StringVar(&s.bookmark3, "bookmark3", "", "deployment history filter (bookmark_parameter3)")

	cmd.Flags().StringVar(&s.uniqueToken, "unique-token", "", "explicit idempotency UUID (default: generate fresh)")
}

var deployCreateSpec = &deployTargetSpec{}

var deployCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a deployment target",
	Args:  cobra.ExactArgs(1),
	Example: `  # tag-only release target, env var bound, auto-promote-only
  sem-ai deploy create release --project sem-ai \
    --tag-regex '^v[0-9].*' \
    --subject-auto \
    --env-var GITHUB_TOKEN=ghp_xxx

  # main-branch-only snapshot target
  sem-ai deploy create snapshot --project my-app --branch-exact main --subject-any`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(deployCreateSpec.projectFlag)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		body, err := deployCreateSpec.toBody(args[0])
		if err != nil {
			output.Error("flag_error", err.Error(), 2)
			return err
		}
		body["project_id"] = projectID
		token := deployCreateSpec.uniqueToken
		if token == "" {
			token = client.NewRequestToken()
		}
		body["unique_token"] = token

		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("deployment_targets", bodyBytes)
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

var deployUpdateSpec = &deployTargetSpec{}
var deployUpdateNameFlag string

var deployUpdateCmd = &cobra.Command{
	Use:   "update <target-id>",
	Short: "Update a deployment target (PATCH)",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai deploy update <target-id> --tag-regex '^v[0-9]+\.[0-9]+\.[0-9]+$' --env-var GITHUB_TOKEN=new_value`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		body, err := deployUpdateSpec.toBody(deployUpdateNameFlag)
		if err != nil {
			output.Error("flag_error", err.Error(), 2)
			return err
		}
		token := deployUpdateSpec.uniqueToken
		if token == "" {
			token = client.NewRequestToken()
		}
		body["unique_token"] = token

		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Patch("deployment_targets", args[0], bodyBytes)
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

func init() {
	deployTargetsCmd.Flags().StringVar(&deployTargetsProjectFlag, "project", "", "project name or ID (auto-detected from git remote if omitted)")

	bindDeploySpecFlags(deployCreateCmd, deployCreateSpec)

	bindDeploySpecFlags(deployUpdateCmd, deployUpdateSpec)
	deployUpdateCmd.Flags().StringVar(&deployUpdateNameFlag, "name", "", "rename the target")

	deployCmd.AddCommand(deployTargetsCmd)
	deployCmd.AddCommand(deployShowCmd)
	deployCmd.AddCommand(deployHistoryCmd)
	deployCmd.AddCommand(deployActivateCmd)
	deployCmd.AddCommand(deployDeactivateCmd)
	deployCmd.AddCommand(deployCreateCmd)
	deployCmd.AddCommand(deployUpdateCmd)
	deployCmd.AddCommand(deployDeleteCmd)
	rootCmd.AddCommand(deployCmd)
}
