package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var testboxCmd = &cobra.Command{
	Use:   "testbox",
	Short: "Run CI commands against local changes in a real Semaphore environment",
	Long: `Testbox spins up a Semaphore CI environment you can run commands against instantly.
Uses Semaphore's debug project API to create a warm VM with your project's
machine type, secrets, and cache — then syncs your local code and executes commands via SSH.`,
}

var (
	testboxWarmupProjectFlag     string
	testboxWarmupMachineFlag     string
	testboxWarmupOSImageFlag     string
	testboxWarmupDurationFlag time.Duration
)

var testboxWarmupCmd = &cobra.Command{
	Use:   "warmup",
	Short: "Start a testbox — warm CI environment for your project",
	Example: `  sem-agent testbox warmup --project my-app
  sem-agent testbox warmup --project my-app --machine f1-standard-4 --duration 30m
  sem-agent testbox warmup --project my-app --os-image ubuntu2204`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		project := testboxWarmupProjectFlag
		if project == "" {
			p, err := detectProject()
			if err != nil {
				output.Error("context_error", "could not detect project — use --project", 1)
				return err
			}
			project = p
		}

		projectID, err := resolveProjectID(project)
		if err != nil {
			output.Error("project_error", err.Error(), 1)
			return err
		}

		c := client.New()

		durationSecs := int(testboxWarmupDurationFlag.Seconds())
		keepalive := fmt.Sprintf("sudo mkdir -p /work/testbox && sudo chown $(whoami) /work/testbox && echo testbox-ready && sleep %d", durationSecs)

		jobSpec := map[string]any{
			"apiVersion": "v1alpha",
			"kind":       "Job",
			"metadata":   map[string]string{"name": "sem-agent testbox"},
			"spec": map[string]any{
				"project_id": projectID,
				"agent": map[string]any{
					"machine": map[string]string{
						"type":     testboxWarmupMachineFlag,
						"os_image": testboxWarmupOSImageFlag,
					},
				},
				"commands": []string{keepalive},
			},
		}
		bodyBytes, _ := json.Marshal(jobSpec)

		resp, err := c.Post("jobs", bodyBytes)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		var job jobStatus
		if err := json.Unmarshal(resp.Body, &job); err != nil {
			output.Error("parse_error", err.Error(), 1)
			return err
		}

		jobID := job.Metadata.ID

		// Poll until RUNNING
		fmt.Fprintf(os.Stderr, "Warming up testbox for %s ", project)
		for i := 0; i < 120; i++ {
			time.Sleep(2 * time.Second)
			fmt.Fprintf(os.Stderr, ".")

			statusResp, err := c.Get("jobs", jobID)
			if err != nil {
				continue
			}
			json.Unmarshal(statusResp.Body, &job)

			if job.Status.State == "FINISHED" {
				fmt.Fprintln(os.Stderr)
				output.Error("testbox_error", "job finished before reaching RUNNING state", 1)
				return fmt.Errorf("job finished prematurely")
			}

			if job.Status.State == "RUNNING" {
				break
			}
		}
		fmt.Fprintln(os.Stderr)

		if job.Status.State != "RUNNING" {
			// Cleanup: stop the stuck job
			c.PostAction("jobs", jobID, "stop", nil)
			output.Error("testbox_error", fmt.Sprintf("job stuck in state %q after timeout — stopped", job.Status.State), 1)
			return fmt.Errorf("warmup timeout")
		}

		// Get SSH key
		sshResp, err := c.Get("jobs", jobID+"/debug_ssh_key")
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var sshKey struct {
			Key string `json:"key"`
		}
		if sshResp.StatusCode == 200 {
			json.Unmarshal(sshResp.Body, &sshKey)
		}

		sshPort := findSSHPort(job)

		result := map[string]any{
			"status":     "ready",
			"testbox_id": jobID,
			"project":    project,
			"machine":    testboxWarmupMachineFlag,
			"os_image":   testboxWarmupOSImageFlag,
			"ssh": map[string]any{
				"ip":   job.Status.Agent.IP,
				"port": sshPort,
				"user": "semaphore",
			},
			"expires_in": testboxWarmupDurationFlag.String(),
			"usage": map[string]string{
				"run":  fmt.Sprintf("sem-agent testbox run --id %s \"your-command\"", jobID),
				"ssh":  fmt.Sprintf("sem-agent testbox ssh --id %s", jobID),
				"stop": fmt.Sprintf("sem-agent testbox stop --id %s", jobID),
			},
		}

		if sshKey.Key != "" {
			keyFile := fmt.Sprintf("/tmp/.sem-testbox-%s.key", jobID)
			os.WriteFile(keyFile, []byte(sshKey.Key), 0600)
			result["ssh_key_file"] = keyFile
		}

		output.Result(result)
		return nil
	},
}

type jobStatus struct {
	Metadata struct {
		ID string `json:"id"`
	} `json:"metadata"`
	Status struct {
		State string `json:"state"`
		Agent struct {
			IP    string `json:"ip"`
			Ports []struct {
				Name   string `json:"name"`
				Number int    `json:"number"`
			} `json:"ports"`
		} `json:"agent"`
	} `json:"status"`
}

func findSSHPort(job jobStatus) int {
	for _, p := range job.Status.Agent.Ports {
		if p.Name == "ssh" {
			return p.Number
		}
	}
	return 0
}

var testboxRunID string

var testboxRunCmd = &cobra.Command{
	Use:   "run <command>",
	Short: "Sync local changes and run a command in the testbox",
	Args:  cobra.MinimumNArgs(1),
	Example: `  sem-agent testbox run --id <testbox-id> "go test ./..."
  sem-agent testbox run --id <testbox-id> "make build"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if testboxRunID == "" {
			output.Error("invalid_args", "--id is required (testbox ID from warmup)", 1)
			return fmt.Errorf("--id is required")
		}

		c := client.New()

		// Get job status for SSH info
		resp, err := c.Get("jobs", testboxRunID)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var job struct {
			Status struct {
				State string `json:"state"`
				Agent struct {
					IP    string `json:"ip"`
					Ports []struct {
						Name   string `json:"name"`
						Number int    `json:"number"`
					} `json:"ports"`
				} `json:"agent"`
			} `json:"status"`
		}
		json.Unmarshal(resp.Body, &job)

		if job.Status.State != "RUNNING" {
			output.Error("testbox_error", fmt.Sprintf("testbox is not running (state: %s)", job.Status.State), 1)
			return fmt.Errorf("testbox not running")
		}

		sshPort := 0
		for _, p := range job.Status.Agent.Ports {
			if p.Name == "ssh" {
				sshPort = p.Number
			}
		}

		keyFile := fmt.Sprintf("/tmp/.sem-testbox-%s.key", testboxRunID)

		// 1. Rsync local changes
		rsyncArgs := []string{
			"-az", "--delete", "--checksum",
			"-e", fmt.Sprintf("ssh -i %s -p %d -o StrictHostKeyChecking=no -o IdentitiesOnly=yes", keyFile, sshPort),
			"./",
			fmt.Sprintf("semaphore@%s:~/code/", job.Status.Agent.IP),
		}

		fmt.Fprintf(os.Stderr, "Syncing local changes...\n")
		rsyncCmd := exec.Command("rsync", rsyncArgs...)
		rsyncCmd.Stderr = os.Stderr
		if err := rsyncCmd.Run(); err != nil {
			output.Error("sync_error", fmt.Sprintf("rsync failed: %s", err), 1)
			return err
		}

		// 2. Execute command via SSH (touch activity file to reset idle timer)
		userCmd := args[0]
		sshArgs := []string{
			"-i", keyFile,
			"-p", fmt.Sprintf("%d", sshPort),
			"-o", "StrictHostKeyChecking=no",
			"-o", "IdentitiesOnly=yes",
			"-t",
			fmt.Sprintf("semaphore@%s", job.Status.Agent.IP),
			fmt.Sprintf("touch /tmp/.testbox-activity && cd ~/code && %s", userCmd),
		}

		sshCmd := exec.Command("ssh", sshArgs...)
		sshCmd.Stdin = os.Stdin
		sshCmd.Stdout = os.Stdout
		sshCmd.Stderr = os.Stderr

		return sshCmd.Run()
	},
}

var testboxSSHID string

var testboxSSHCmd = &cobra.Command{
	Use:     "ssh",
	Short:   "Open an interactive SSH session to the testbox",
	Example: `  sem-agent testbox ssh --id <testbox-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if testboxSSHID == "" {
			output.Error("invalid_args", "--id is required", 1)
			return fmt.Errorf("--id is required")
		}

		c := client.New()
		resp, err := c.Get("jobs", testboxSSHID)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		var job struct {
			Status struct {
				State string `json:"state"`
				Agent struct {
					IP    string `json:"ip"`
					Ports []struct {
						Name   string `json:"name"`
						Number int    `json:"number"`
					} `json:"ports"`
				} `json:"agent"`
			} `json:"status"`
		}
		json.Unmarshal(resp.Body, &job)

		if job.Status.State != "RUNNING" {
			output.Error("testbox_error", fmt.Sprintf("testbox not running (state: %s)", job.Status.State), 1)
			return fmt.Errorf("testbox not running")
		}

		sshPort := 0
		for _, p := range job.Status.Agent.Ports {
			if p.Name == "ssh" {
				sshPort = p.Number
			}
		}

		keyFile := fmt.Sprintf("/tmp/.sem-testbox-%s.key", testboxSSHID)

		sshArgs := []string{
			"-i", keyFile,
			"-p", fmt.Sprintf("%d", sshPort),
			"-o", "StrictHostKeyChecking=no",
			"-o", "IdentitiesOnly=yes",
			"-t",
			fmt.Sprintf("semaphore@%s", job.Status.Agent.IP),
			"bash /tmp/ssh_jump_point",
		}

		sshCmd := exec.Command("ssh", sshArgs...)
		sshCmd.Stdin = os.Stdin
		sshCmd.Stdout = os.Stdout
		sshCmd.Stderr = os.Stderr

		return sshCmd.Run()
	},
}

var testboxStopID string

var testboxStopCmd = &cobra.Command{
	Use:     "stop",
	Short:   "Stop a running testbox",
	Example: `  sem-agent testbox stop --id <testbox-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if testboxStopID == "" {
			output.Error("invalid_args", "--id is required", 1)
			return fmt.Errorf("--id is required")
		}

		c := client.New()
		resp, err := c.Post(fmt.Sprintf("jobs/%s/stop", testboxStopID), nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		// Clean up SSH key
		keyFile := fmt.Sprintf("/tmp/.sem-testbox-%s.key", testboxStopID)
		os.Remove(keyFile)

		output.Result(map[string]string{
			"status":     "stopped",
			"testbox_id": testboxStopID,
		})
		return nil
	},
}

func init() {
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupProjectFlag, "project", "", "project name (auto-detected from git)")
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupMachineFlag, "machine", "f1-standard-2", "machine type (f1-standard-2, f1-standard-4, etc.)")
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupOSImageFlag, "os-image", "ubuntu2204", "OS image (ubuntu2204, ubuntu2404)")
	testboxWarmupCmd.Flags().DurationVar(&testboxWarmupDurationFlag, "duration", 30*time.Minute, "max session duration")

	testboxRunCmd.Flags().StringVar(&testboxRunID, "id", "", "testbox ID (from warmup)")
	testboxSSHCmd.Flags().StringVar(&testboxSSHID, "id", "", "testbox ID")
	testboxStopCmd.Flags().StringVar(&testboxStopID, "id", "", "testbox ID")

	testboxCmd.AddCommand(testboxWarmupCmd)
	testboxCmd.AddCommand(testboxRunCmd)
	testboxCmd.AddCommand(testboxSSHCmd)
	testboxCmd.AddCommand(testboxStopCmd)
	rootCmd.AddCommand(testboxCmd)
}
