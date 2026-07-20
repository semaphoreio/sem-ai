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
		return emitJSON(resp)
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
		return emitJSON(resp)
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
		c := client.New()

		name := groupUpdateNameFlag
		description := groupUpdateDescFlag

		// The server's group update is a full replace: an omitted name or
		// description is written as an empty string rather than preserved, and
		// the API exposes no single-group GET. So whenever either flag is left
		// unset, fetch the current group and carry its value through — otherwise
		// an --add/--remove-only update would blank the group's name and
		// description.
		if name == "" || description == "" {
			cur, err := fetchGroup(c, args[0])
			if err != nil {
				output.Error("api_error", err.Error(), 1)
				return err
			}
			if name == "" {
				name = cur.Name
			}
			if description == "" {
				description = cur.Description
			}
		}

		body := map[string]any{
			"name":        name,
			"description": description,
		}
		if groupAddFlag != "" {
			body["members_to_add"] = splitCommaList(groupAddFlag)
		}
		if groupRemoveFlag != "" {
			body["members_to_remove"] = splitCommaList(groupRemoveFlag)
		}
		bodyBytes, _ := json.Marshal(body)
		resp, err := c.Patch("groups", args[0], bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
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

type groupRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []string `json:"member_ids"`
}

// fetchGroup returns the current state of a group by id. The org-management API
// exposes no single-group GET, so it lists groups and matches on id. It errors
// when the group is not found, so an update never proceeds blindly against a
// missing group (which the server would answer by blanking its fields). Only
// the first page of groups is consulted, matching the `group list` command.
func fetchGroup(c *client.Client, id string) (*groupRecord, error) {
	resp, err := c.List("groups")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	// List responses are shaped {"groups": [...]}; tolerate a bare array too.
	var wrapped struct {
		Groups []groupRecord `json:"groups"`
	}
	groups := []groupRecord{}
	if json.Unmarshal(resp.Body, &wrapped) == nil && wrapped.Groups != nil {
		groups = wrapped.Groups
	} else if err := json.Unmarshal(resp.Body, &groups); err != nil {
		return nil, fmt.Errorf("could not parse groups list: %w", err)
	}
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i], nil
		}
	}
	return nil, fmt.Errorf("group %q not found", id)
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
