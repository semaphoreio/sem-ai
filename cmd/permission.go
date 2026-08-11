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
	Short: "List the permission types defined for a scope (the catalog, NOT your grants)",
	Long: `List the catalog of permission types the platform defines for a scope.

IMPORTANT: this returns every permission that EXISTS for the org/project
scope (e.g. project.job.rerun, project.view). It does NOT tell you which of
them the current token's user actually holds — it is not a "what am I allowed
to do" check. The public API does not currently expose per-user effective
permissions (server-side authorization uses an internal ListUserPermissions
RBAC call that has no public v1alpha endpoint), so do not use this output to
decide whether an action will be permitted. Attempt the action and handle the
failure instead — note this API masks an RBAC denial as HTTP 404 "Not Found",
not 403, so a denial is indistinguishable from a missing resource.`,
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
