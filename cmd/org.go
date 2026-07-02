package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Organization management — members, roles",
}

var orgMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Organization member operations",
}

var orgMemberListCmd = &cobra.Command{
	Use:   "list",
	Short: "List organization members",
	Example: `  sem-ai org member list
  sem-ai org member list --type service_account
  sem-ai org member list --type group`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		params := url.Values{}
		if memberTypeFlag != "" {
			params.Set("member_type", memberTypeFlag)
		}
		resp, err := c.ListWithParams("members", params)
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

var orgRoleCmd = &cobra.Command{
	Use:   "role",
	Short: "Organization role operations",
}

var orgRoleListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List organization roles",
	Example: `  sem-ai org role list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.List("roles")
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

var orgRoleShowCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show role details",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai org role show <role-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Get("roles", args[0])
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

var orgRoleCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a custom role",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai org role create deployer --permissions "project.view,project.job.rerun"
  sem-ai org role create viewer --scope project --permissions "project.view"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]any{
			"name":        args[0],
			"description": roleDescFlag,
			"scope":       roleScopeFlag,
			"permissions": splitCommaList(rolePermissionsFlag),
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("roles", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		var result any
		json.Unmarshal(resp.Body, &result)
		output.Result(result)
		return nil
	},
}

var orgRoleUpdateCmd = &cobra.Command{
	Use:     "update <id>",
	Short:   "Update a custom role",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai org role update <role-id> --permissions "project.view,project.job.rerun"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]any{}
		if roleUpdateNameFlag != "" {
			body["name"] = roleUpdateNameFlag
		}
		if roleUpdateDescFlag != "" {
			body["description"] = roleUpdateDescFlag
		}
		if roleUpdatePermissionsFlag != "" {
			body["permissions"] = splitCommaList(roleUpdatePermissionsFlag)
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Patch("roles", args[0], bodyBytes)
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

var orgRoleDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a custom role",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai org role delete <role-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Delete("roles", args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}
		output.Result(map[string]string{"status": "deleted", "id": args[0]})
		return nil
	},
}

var memberSetRoleCmd = &cobra.Command{
	Use:   "set-role <subject-id> <role-id>",
	Short: "Set an org-level role for a member or service account",
	Args:  cobra.ExactArgs(2),
	Example: `  sem-ai org member set-role <user-id> <role-id>
  sem-ai org member set-role <service-account-id> <role-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]string{"role_id": args[1]}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Put("members/"+args[0]+"/role", bodyBytes)
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

var memberRemoveCmd = &cobra.Command{
	Use:     "remove <subject-id>",
	Short:   "Remove a member or service account from the organization",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai org member remove <user-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Delete("members", args[0])
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

var memberAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Invite a person to the organization by SCM handle",
	Example: `  sem-ai org member add --provider github --handle octocat
  sem-ai org member add --provider github --handle octocat --role <role-id> --name "Octo Cat" --email octo@example.com
  sem-ai org member add --provider bitbucket --handle jdoe --uid 557058:1a2b3c`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]string{}
		if memberAddProviderFlag != "" {
			body["provider"] = memberAddProviderFlag
		}
		if memberAddHandleFlag != "" {
			body["handle"] = memberAddHandleFlag
		}
		if memberAddUIDFlag != "" {
			body["uid"] = memberAddUIDFlag
		}
		if memberAddRoleFlag != "" {
			body["role_id"] = memberAddRoleFlag
		}
		if memberAddNameFlag != "" {
			body["name"] = memberAddNameFlag
		}
		if memberAddEmailFlag != "" {
			body["email"] = memberAddEmailFlag
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("members", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
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
	roleDescFlag              string
	roleScopeFlag             string
	rolePermissionsFlag       string
	roleUpdateNameFlag        string
	roleUpdateDescFlag        string
	roleUpdatePermissionsFlag string
	memberTypeFlag            string
	memberAddProviderFlag     string
	memberAddHandleFlag       string
	memberAddUIDFlag          string
	memberAddRoleFlag         string
	memberAddNameFlag         string
	memberAddEmailFlag        string
)

func splitCommaList(s string) []string {
	if s == "" {
		return []string{}
	}
	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func init() {
	orgRoleCreateCmd.Flags().StringVar(&roleDescFlag, "description", "", "role description")
	orgRoleCreateCmd.Flags().StringVar(&roleScopeFlag, "scope", "org", "role scope: org or project")
	orgRoleCreateCmd.Flags().StringVar(&rolePermissionsFlag, "permissions", "", "comma-separated permissions")

	orgRoleUpdateCmd.Flags().StringVar(&roleUpdateNameFlag, "name", "", "new role name")
	orgRoleUpdateCmd.Flags().StringVar(&roleUpdateDescFlag, "description", "", "new role description")
	orgRoleUpdateCmd.Flags().StringVar(&roleUpdatePermissionsFlag, "permissions", "", "comma-separated permissions")

	orgMemberListCmd.Flags().StringVar(&memberTypeFlag, "type", "", "filter by member type: user|service_account|group (default user)")

	memberAddCmd.Flags().StringVar(&memberAddHandleFlag, "handle", "", "SCM login/handle of the person to invite (required)")
	memberAddCmd.Flags().StringVar(&memberAddProviderFlag, "provider", "", "SCM provider: github|bitbucket|gitlab (required)")
	memberAddCmd.Flags().StringVar(&memberAddUIDFlag, "uid", "", "SCM user id (required for bitbucket)")
	memberAddCmd.Flags().StringVar(&memberAddRoleFlag, "role", "", "org role id to assign")
	memberAddCmd.Flags().StringVar(&memberAddNameFlag, "name", "", "display name")
	memberAddCmd.Flags().StringVar(&memberAddEmailFlag, "email", "", "email address")
	_ = memberAddCmd.MarkFlagRequired("handle")
	_ = memberAddCmd.MarkFlagRequired("provider")

	orgMemberCmd.AddCommand(orgMemberListCmd)
	orgRoleCmd.AddCommand(orgRoleListCmd)
	orgRoleCmd.AddCommand(orgRoleShowCmd)
	orgRoleCmd.AddCommand(orgRoleCreateCmd)
	orgRoleCmd.AddCommand(orgRoleUpdateCmd)
	orgRoleCmd.AddCommand(orgRoleDeleteCmd)
	orgMemberCmd.AddCommand(memberSetRoleCmd)
	orgMemberCmd.AddCommand(memberRemoveCmd)
	orgMemberCmd.AddCommand(memberAddCmd)
	orgCmd.AddCommand(orgMemberCmd)
	orgCmd.AddCommand(orgRoleCmd)
	rootCmd.AddCommand(orgCmd)
}
