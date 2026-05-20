package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var projectMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Project member operations",
}

var projectMemberListCmd = &cobra.Command{
	Use:   "list <project>",
	Short: "List project members",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai project member list my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(args[0])
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		u := fmt.Sprintf("projects/%s/members", projectID)
		resp, err := c.List(u)
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

var projectMemberAssignRoleCmd = &cobra.Command{
	Use:   "assign-role <project> <subject-id> <role-id>",
	Short: "Assign a project-level role to a member",
	Args:  cobra.ExactArgs(3),
	Example: `  sem-ai project member assign-role my-project <user-id> <role-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(args[0])
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		body := map[string]string{"role_id": args[2]}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		u := fmt.Sprintf("projects/%s/members/%s/roles", projectID, args[1])
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

var projectMemberRetractRoleCmd = &cobra.Command{
	Use:   "retract-role <project> <subject-id>",
	Short: "Remove project-level role from a member",
	Args:  cobra.ExactArgs(2),
	Example: `  sem-ai project member retract-role my-project <user-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		projectID, err := resolveProjectID(args[0])
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}
		c := client.New()
		u := fmt.Sprintf("projects/%s/members/%s/roles", projectID, args[1])
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

func init() {
	projectMemberCmd.AddCommand(projectMemberListCmd)
	projectMemberCmd.AddCommand(projectMemberAssignRoleCmd)
	projectMemberCmd.AddCommand(projectMemberRetractRoleCmd)
	projectCmd.AddCommand(projectMemberCmd)
}
