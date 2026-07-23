package cmd

import (
	"fmt"
	"net/url"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var permissionCmd = &cobra.Command{
	Use:   "permission",
	Short: "Permission operations",
}

var permissionScopeFlag string

var permissionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available permissions",
	Example: `  sem-ai permission list
  sem-ai permission list --scope project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-ai connect' first")
		}
		c := client.New()
		params := url.Values{}
		if permissionScopeFlag != "" {
			params.Set("scope", permissionScopeFlag)
		}
		var resp *client.Response
		var err error
		if len(params) > 0 {
			resp, err = c.ListWithParams("permissions", params)
		} else {
			resp, err = c.List("permissions")
		}
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		return emitJSON(resp)
	},
}

func init() {
	permissionListCmd.Flags().StringVar(&permissionScopeFlag, "scope", "", "filter by scope: org or project")

	permissionCmd.AddCommand(permissionListCmd)
	rootCmd.AddCommand(permissionCmd)
}
