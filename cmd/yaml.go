package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var yamlCmd = &cobra.Command{
	Use:   "yaml",
	Short: "Pipeline YAML tools",
}

var yamlValidateFileFlag string

var yamlValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a pipeline YAML file against the Semaphore API",
	Example: `  sem-ai yaml validate --file .semaphore/semaphore.yml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		if yamlValidateFileFlag == "" {
			output.Error("invalid_args", "--file is required", 1)
			return fmt.Errorf("--file is required")
		}

		data, err := os.ReadFile(yamlValidateFileFlag)
		if err != nil {
			output.Error("file_error", fmt.Sprintf("cannot read %s: %s", yamlValidateFileFlag, err), 1)
			return err
		}

		// API expects YAML as the POST body
		c := client.New()
		resp, err := c.PostYAML("yaml", data)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		if resp.StatusCode == 200 {
			output.Result(map[string]any{
				"valid":   true,
				"file":    yamlValidateFileFlag,
				"message": "pipeline YAML is valid",
			})
			return nil
		}

		// Validation errors
		var result any
		if json.Unmarshal(resp.Body, &result) == nil {
			output.Result(map[string]any{
				"valid":  false,
				"file":   yamlValidateFileFlag,
				"errors": result,
			})
		} else {
			output.Result(map[string]any{
				"valid":   false,
				"file":    yamlValidateFileFlag,
				"message": string(resp.Body),
			})
		}
		return fmt.Errorf("validation failed")
	},
}

func init() {
	yamlValidateCmd.Flags().StringVar(&yamlValidateFileFlag, "file", "", "path to pipeline YAML file (required)")
	yamlCmd.AddCommand(yamlValidateCmd)
	rootCmd.AddCommand(yamlCmd)
}
