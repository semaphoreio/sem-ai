package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var artifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Artifact operations — list and download build artifacts",
}

var (
	artifactScope   string
	artifactScopeID string
)

var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts for a job, workflow, or project",
	Example: `  sem-agent artifact list --scope jobs --id <job-id>
  sem-agent artifact list --scope workflows --id <workflow-id>
  sem-agent artifact list --scope projects --id <project-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		if artifactScope == "" || artifactScopeID == "" {
			output.Error("invalid_args", "--scope and --id are required", 1)
			return fmt.Errorf("--scope and --id are required")
		}

		c := client.New()
		params := url.Values{}
		params.Set("scope", artifactScope)
		params.Set("scope_id", artifactScopeID)

		resp, err := c.ListWithParams("artifacts", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var result any
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}
		output.Result(result)
		return nil
	},
}

var (
	artifactGetScope   string
	artifactGetScopeID string
	artifactGetPath    string
	artifactGetOutput  string
)

var artifactGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Download an artifact via signed URL",
	Example: `  sem-agent artifact get --scope jobs --id <job-id> --path agent/job_logs.txt.gz
  sem-agent artifact get --scope jobs --id <job-id> --path test-results/junit.json --output ./results.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		if artifactGetScope == "" || artifactGetScopeID == "" || artifactGetPath == "" {
			output.Error("invalid_args", "--scope, --id, and --path are required", 1)
			return fmt.Errorf("--scope, --id, and --path are required")
		}

		c := client.New()

		// Get signed URL
		params := url.Values{}
		params.Set("scope", artifactGetScope)
		params.Set("scope_id", artifactGetScopeID)
		params.Set("path", artifactGetPath)
		params.Set("method", "GET")

		resp, err := c.ListWithParams("artifacts/signed_url", params)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var signedResp struct {
			Items []struct {
				Path string `json:"path"`
				URL  string `json:"url"`
			} `json:"items"`
		}
		if err := json.Unmarshal(resp.Body, &signedResp); err != nil || len(signedResp.Items) == 0 {
			output.Error("not_found", "artifact not found", 404)
			return fmt.Errorf("artifact not found")
		}

		// Download
		dlResp, err := c.GetExternal(signedResp.Items[0].URL)
		if err != nil {
			output.Error("download_error", err.Error(), 1)
			return err
		}
		if dlResp.StatusCode != 200 {
			output.Error("download_error", fmt.Sprintf("HTTP %d", dlResp.StatusCode), dlResp.StatusCode)
			return fmt.Errorf("download failed: HTTP %d", dlResp.StatusCode)
		}

		data := dlResp.Body

		// Auto-decompress gzip
		if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			r, err := gzip.NewReader(bytes.NewReader(data))
			if err == nil {
				decompressed, err := io.ReadAll(r)
				r.Close()
				if err == nil {
					data = decompressed
				}
			}
		}

		// Write to file or stdout
		if artifactGetOutput != "" {
			dir := filepath.Dir(artifactGetOutput)
			if dir != "." {
				os.MkdirAll(dir, 0755)
			}
			if err := os.WriteFile(artifactGetOutput, data, 0644); err != nil {
				output.Error("write_error", err.Error(), 1)
				return err
			}
			output.Result(map[string]any{
				"status": "downloaded",
				"path":   artifactGetPath,
				"output": artifactGetOutput,
				"size":   len(data),
			})
		} else {
			// Output raw content to stdout
			os.Stdout.Write(data)
		}
		return nil
	},
}

func init() {
	artifactListCmd.Flags().StringVar(&artifactScope, "scope", "", "artifact scope: jobs, workflows, or projects")
	artifactListCmd.Flags().StringVar(&artifactScopeID, "id", "", "scope ID (job/workflow/project UUID)")

	artifactGetCmd.Flags().StringVar(&artifactGetScope, "scope", "", "artifact scope: jobs, workflows, or projects")
	artifactGetCmd.Flags().StringVar(&artifactGetScopeID, "id", "", "scope ID (job/workflow/project UUID)")
	artifactGetCmd.Flags().StringVar(&artifactGetPath, "path", "", "artifact path within scope")
	artifactGetCmd.Flags().StringVar(&artifactGetOutput, "output", "", "output file path (default: stdout)")

	artifactCmd.AddCommand(artifactListCmd)
	artifactCmd.AddCommand(artifactGetCmd)
	rootCmd.AddCommand(artifactCmd)
}
