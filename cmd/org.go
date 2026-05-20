package cmd

import (
	"encoding/json"
	"fmt"
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
	Use:     "list",
	Short:   "List organization members",
	Example: `  sem-ai org member list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.List("members")
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
	Use:   "update <id>",
	Short: "Update a custom role",
	Args:  cobra.ExactArgs(1),
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

var memberAssignRoleCmd = &cobra.Command{
	Use:   "assign-role <subject-id> <role-id>",
	Short: "Assign an org-level role to a member or service account",
	Args:  cobra.ExactArgs(2),
	Example: `  sem-ai org member assign-role <user-id> <role-id>
  sem-ai org member assign-role <service-account-id> <role-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]string{"role_id": args[1]}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		u := fmt.Sprintf("members/%s/roles", args[0])
		resp, err := c.Post(u, bodyBytes)
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

var memberRetractRoleCmd = &cobra.Command{
	Use:     "retract-role <subject-id>",
	Short:   "Remove org-level role from a member or service account",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai org member retract-role <user-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		u := fmt.Sprintf("members/%s/roles", args[0])
		resp, err := c.DeletePath(u)
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


var (
	roleDescFlag              string
	roleScopeFlag             string
	rolePermissionsFlag       string
	roleUpdateNameFlag        string
	roleUpdateDescFlag        string
	roleUpdatePermissionsFlag string
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

	orgMemberCmd.AddCommand(orgMemberListCmd)
	orgRoleCmd.AddCommand(orgRoleListCmd)
	orgRoleCmd.AddCommand(orgRoleShowCmd)
	orgRoleCmd.AddCommand(orgRoleCreateCmd)
	orgRoleCmd.AddCommand(orgRoleUpdateCmd)
	orgRoleCmd.AddCommand(orgRoleDeleteCmd)
	orgMemberCmd.AddCommand(memberAssignRoleCmd)
	orgMemberCmd.AddCommand(memberRetractRoleCmd)
	orgCmd.AddCommand(orgMemberCmd)
	orgCmd.AddCommand(orgRoleCmd)
	rootCmd.AddCommand(orgCmd)
}
