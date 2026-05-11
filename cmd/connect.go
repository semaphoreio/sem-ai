package cmd

import (
	"fmt"
	"strings"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var connectCmd = &cobra.Command{
	Use:   "connect <host> <token>",
	Short: "Connect to a Semaphore organization",
	Args:  cobra.ExactArgs(2),
	Example: `  sem-agent connect myorg.semaphoreci.com YOUR_API_TOKEN`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := args[0]
		token := args[1]

		// Verify connection
		c := client.NewWithConfig(token, host)
		resp, err := c.List("projects")
		if err != nil {
			output.Error("connect_error", fmt.Sprintf("failed to connect to %s: %s", host, err), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("connect_error", fmt.Sprintf("authentication failed (HTTP %d)", resp.StatusCode), resp.StatusCode)
			return fmt.Errorf("authentication failed")
		}

		// Save to config
		name := strings.ReplaceAll(host, ".", "_")
		viper.Set("active-context", name)
		viper.Set(fmt.Sprintf("contexts.%s.auth.token", name), token)
		viper.Set(fmt.Sprintf("contexts.%s.host", name), host)
		if err := viper.WriteConfig(); err != nil {
			output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
			return err
		}

		output.Result(map[string]string{
			"status":  "connected",
			"host":    host,
			"context": name,
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
