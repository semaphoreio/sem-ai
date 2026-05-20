package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Group management — organize members into teams",
}

var groupListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List groups in the organization",
	Example: `  sem-ai group list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.List("groups")
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

var groupCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a group",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai group create backend-team
  sem-ai group create backend-team --description "Backend engineers" --members "id1,id2"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]any{
			"name":        args[0],
			"description": groupDescFlag,
			"member_ids":  splitCommaList(groupMembersFlag),
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Post("groups", bodyBytes)
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

var groupUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a group — add or remove members",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-ai group update <group-id> --add "id1,id2" --remove "id3"
  sem-ai group update <group-id> --name "new-name"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		body := map[string]any{}
		if groupUpdateNameFlag != "" {
			body["name"] = groupUpdateNameFlag
		}
		if groupUpdateDescFlag != "" {
			body["description"] = groupUpdateDescFlag
		}
		if groupAddFlag != "" {
			body["members_to_add"] = splitCommaList(groupAddFlag)
		}
		if groupRemoveFlag != "" {
			body["members_to_remove"] = splitCommaList(groupRemoveFlag)
		}
		bodyBytes, _ := json.Marshal(body)
		c := client.New()
		resp, err := c.Patch("groups", args[0], bodyBytes)
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

var groupDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a group",
	Args:    cobra.ExactArgs(1),
	Example: `  sem-ai group delete <group-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		resp, err := c.Delete("groups", args[0])
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

var (
	groupDescFlag       string
	groupMembersFlag    string
	groupUpdateNameFlag string
	groupUpdateDescFlag string
	groupAddFlag        string
	groupRemoveFlag     string
)

func init() {
	groupCreateCmd.Flags().StringVar(&groupDescFlag, "description", "", "group description")
	groupCreateCmd.Flags().StringVar(&groupMembersFlag, "members", "", "comma-separated member IDs")

	groupUpdateCmd.Flags().StringVar(&groupUpdateNameFlag, "name", "", "new group name")
	groupUpdateCmd.Flags().StringVar(&groupUpdateDescFlag, "description", "", "new group description")
	groupUpdateCmd.Flags().StringVar(&groupAddFlag, "add", "", "comma-separated member IDs to add")
	groupUpdateCmd.Flags().StringVar(&groupRemoveFlag, "remove", "", "comma-separated member IDs to remove")

	groupCmd.AddCommand(groupListCmd)
	groupCmd.AddCommand(groupCreateCmd)
	groupCmd.AddCommand(groupUpdateCmd)
	groupCmd.AddCommand(groupDeleteCmd)
	rootCmd.AddCommand(groupCmd)
}
