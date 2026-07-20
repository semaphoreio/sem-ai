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
		return emitJSON(resp)
	},
}

var projectMemberSetRoleCmd = &cobra.Command{
	Use:   "set-role <project> <subject-id> <role-id>",
	Short: "Set a project-level role for a member",
	Args:  cobra.ExactArgs(3),
	Example: `  sem-ai project member set-role my-project <user-id> <role-id>`,
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
		resp, err := c.Put("projects/"+projectID+"/members/"+args[1]+"/role", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

var projectMemberRemoveCmd = &cobra.Command{
	Use:   "remove <project> <subject-id>",
	Short: "Remove project-level role from a member",
	Args:  cobra.ExactArgs(2),
	Example: `  sem-ai project member remove my-project <user-id>`,
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
		u := fmt.Sprintf("projects/%s/members/%s/role", projectID, args[1])
		resp, err := c.DeletePath(u)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

func init() {
	projectMemberCmd.AddCommand(projectMemberListCmd)
	projectMemberCmd.AddCommand(projectMemberSetRoleCmd)
	projectMemberCmd.AddCommand(projectMemberRemoveCmd)
	projectCmd.AddCommand(projectMemberCmd)
}
