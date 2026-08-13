package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

// testboxJobName is the fixed debug-job name testbox uses for every warmup —
// it's how `testbox list` tells testboxes apart from other running jobs.
const testboxJobName = "sem-ai testbox"

var testboxCmd = &cobra.Command{
	Use:   "testbox",
	Short: "Run CI commands against local changes in a real Semaphore environment",
	Long: `Testbox spins up a Semaphore CI environment you can run commands against instantly.
Uses Semaphore's debug project API to create a warm VM with your project's
machine type, secrets, and cache, then syncs your local code and executes commands via SSH.`,
}

var (
	testboxWarmupProjectFlag  string
	testboxWarmupMachineFlag  string
	testboxWarmupOSImageFlag  string
	testboxWarmupDurationFlag time.Duration
)

var testboxWarmupCmd = &cobra.Command{
	Use:   "warmup",
	Short: "Start a testbox: warm CI environment for your project",
	Example: `  sem-ai testbox warmup --project my-app
  sem-ai testbox warmup --project my-app --machine f1-standard-4 --duration 30m
  sem-ai testbox warmup --project my-app --os-image ubuntu2204`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}

		project := testboxWarmupProjectFlag
		if project == "" {
			p, err := detectProject()
			if err != nil {
				output.Error("context_error", "could not detect project; use --project", 1)
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
			"metadata":   map[string]string{"name": testboxJobName},
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

		if probe, perr := c.Get("jobs", jobID+"/debug_ssh_key"); perr == nil && probe.StatusCode == 403 {
			c.PostAction("jobs", jobID, "stop", nil)
			output.Error("testbox_error", debugDisabledMsg(project, probe.Body), 1)
			return fmt.Errorf("debug sessions disabled for project %q", project)
		}

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
			output.Error("testbox_error", fmt.Sprintf("job stuck in state %q after timeout; stopped", job.Status.State), 1)
			return fmt.Errorf("warmup timeout")
		}

		sshResp, err := c.Get("jobs", jobID+"/debug_ssh_key")
		if err != nil {
			c.PostAction("jobs", jobID, "stop", nil)
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if sshResp.StatusCode != 200 {
			c.PostAction("jobs", jobID, "stop", nil)
			if sshResp.StatusCode == 403 {
				output.Error("testbox_error", debugDisabledMsg(project, sshResp.Body), 1)
			} else {
				output.Error("testbox_error", fmt.Sprintf("could not get SSH debug key (HTTP %d): %s", sshResp.StatusCode, string(sshResp.Body)), 1)
			}
			return fmt.Errorf("SSH key unavailable")
		}

		var sshKey struct {
			Key string `json:"key"`
		}
		json.Unmarshal(sshResp.Body, &sshKey)
		if sshKey.Key == "" {
			c.PostAction("jobs", jobID, "stop", nil)
			output.Error("testbox_error", "SSH debug key was empty; debug sessions may be disabled for this project", 1)
			return fmt.Errorf("empty SSH key")
		}

		keyFile := fmt.Sprintf("/tmp/.sem-testbox-%s.key", jobID)
		if err := os.WriteFile(keyFile, []byte(sshKey.Key), 0600); err != nil {
			c.PostAction("jobs", jobID, "stop", nil)
			output.Error("testbox_error", fmt.Sprintf("could not write SSH key file: %s", err), 1)
			return err
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
			"expires_in":   testboxWarmupDurationFlag.String(),
			"ssh_key_file": keyFile,
			"usage": map[string]string{
				"run":  fmt.Sprintf("sem-ai testbox run --id %s \"your-command\"", jobID),
				"ssh":  fmt.Sprintf("sem-ai testbox ssh --id %s", jobID),
				"stop": fmt.Sprintf("sem-ai testbox stop --id %s", jobID),
			},
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

func debugDisabledMsg(project string, body []byte) string {
	msg := fmt.Sprintf(
		"debug/SSH sessions are disabled for project %q; testbox needs them.\n"+
			"Enable \"Collaborators can start empty debug sessions\" under the project's "+
			"Settings → Permissions, or ask an organization admin to enable it.",
		project)
	if len(body) > 0 {
		msg += fmt.Sprintf("\nServer response: %s", string(body))
	}
	return msg
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
	Example: `  sem-ai testbox run --id <testbox-id> "go test ./..."
  sem-ai testbox run --id <testbox-id> "make build"`,
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
	Example: `  sem-ai testbox ssh --id <testbox-id>`,
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

var (
	testboxStopID           string
	testboxStopTimeoutFlag  time.Duration
	testboxStopPollInterval = 2 * time.Second // var, not const, so tests can shrink it
)

var testboxStopCmd = &cobra.Command{
	Use:     "stop",
	Short:   "Stop a running testbox",
	Example: `  sem-ai testbox stop --id <testbox-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if testboxStopID == "" {
			output.Error("invalid_args", "--id is required", 1)
			return fmt.Errorf("--id is required")
		}

		c := client.New()

		// If the box is already gone or already finished, don't bother
		// issuing a stop — just report the current state cleanly.
		if state, _, err := jobStateAndResult(c, testboxStopID); err == nil && state == "FINISHED" {
			removeTestboxKeyFile(testboxStopID)
			output.Result(map[string]string{
				"status":     "already_stopped",
				"testbox_id": testboxStopID,
				"state":      state,
			})
			return nil
		}

		resp, err := c.Post(fmt.Sprintf("jobs/%s/stop", testboxStopID), nil)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		if resp.StatusCode == 404 {
			// Job no longer exists server-side — nothing left to stop.
			removeTestboxKeyFile(testboxStopID)
			output.Result(map[string]string{
				"status":     "not_found",
				"testbox_id": testboxStopID,
			})
			return nil
		}
		if resp.StatusCode != 200 {
			output.Error("api_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body)), resp.StatusCode)
			return fmt.Errorf("API returned %d", resp.StatusCode)
		}

		removeTestboxKeyFile(testboxStopID)

		// The stop endpoint only acknowledges the request; the job itself
		// stops asynchronously. Poll briefly to confirm it actually left
		// RUNNING before reporting success, instead of taking the 200 on
		// faith (that's what made the old "stopped" claim unreliable).
		state, result := pollJobStopped(c, testboxStopID, testboxStopTimeoutFlag)

		out := map[string]string{
			"testbox_id": testboxStopID,
			"state":      state,
		}
		if result != "" {
			out["result"] = result
		}
		if state == "FINISHED" {
			out["status"] = "stopped"
		} else {
			// Stop was accepted but not confirmed within the timeout — still
			// truthful, not a false "stopped".
			out["status"] = "stop_requested"
			out["message"] = fmt.Sprintf("stop accepted but not confirmed within %s; check 'sem-ai job show %s'", testboxStopTimeoutFlag, testboxStopID)
		}
		output.Result(out)
		return nil
	},
}

// jobStateAndResult fetches a job's current status.state/status.result.
func jobStateAndResult(c *client.Client, id string) (state, result string, err error) {
	resp, err := c.Get("jobs", id)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var job struct {
		Status struct {
			State  string `json:"state"`
			Result string `json:"result"`
		} `json:"status"`
	}
	if err := json.Unmarshal(resp.Body, &job); err != nil {
		return "", "", err
	}
	return job.Status.State, job.Status.Result, nil
}

// pollJobStopped polls a job until it reaches a terminal state (FINISHED) or
// the timeout elapses, returning the last observed state/result. Bounded and
// short by design — this confirms the stop landed, it doesn't wait out a
// slow-draining job forever.
func pollJobStopped(c *client.Client, id string, timeout time.Duration) (state, result string) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		s, r, err := jobStateAndResult(c, id)
		if err == nil {
			state, result = s, r
			if state == "FINISHED" {
				return state, result
			}
		}
		if time.Now().After(deadline) {
			return state, result
		}
		time.Sleep(testboxStopPollInterval)
	}
}

func removeTestboxKeyFile(id string) {
	os.Remove(fmt.Sprintf("/tmp/.sem-testbox-%s.key", id))
}

// testboxJobEntry decodes the fields of a job list entry that `testbox list`
// surfaces: id/name/create_time from metadata, project + machine type from
// spec, current state from status.
type testboxJobEntry struct {
	Metadata struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		CreateTime string `json:"create_time"`
	} `json:"metadata"`
	Spec struct {
		ProjectID string `json:"project_id"`
		Agent     struct {
			Machine struct {
				Type string `json:"type"`
			} `json:"machine"`
		} `json:"agent"`
	} `json:"spec"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

// maxTestboxListPages bounds the next_page_token pagination loop as a safety
// valve against a pathological/looping server response. 200 pages at up to
// 30 jobs/page comfortably covers any real org's concurrent RUNNING jobs.
const maxTestboxListPages = 200

// fetchRunningJobs pages through GET /jobs?states=RUNNING to completion,
// following the response body's next_page_token until the API stops
// returning one. This is a DIFFERENT pagination convention than
// client.ListAll (which follows the Link/x-has-more headers) — the jobs
// endpoint caps page_size at 30 and embeds its cursor in the body, so a page-1-only
// fetch would silently drop older testboxes on a busy org.
func fetchRunningJobs(c *client.Client) ([]testboxJobEntry, error) {
	var all []testboxJobEntry
	pageToken := ""
	for page := 0; page < maxTestboxListPages; page++ {
		params := url.Values{}
		params.Set("states", "RUNNING")
		if pageToken != "" {
			params.Set("page_token", pageToken)
		}
		resp, err := c.ListWithParams("jobs", params)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
		}

		var body struct {
			Jobs          []testboxJobEntry `json:"jobs"`
			NextPageToken string            `json:"next_page_token"`
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			return nil, err
		}
		all = append(all, body.Jobs...)

		if body.NextPageToken == "" {
			break
		}
		pageToken = body.NextPageToken
	}
	return all, nil
}

var testboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active testboxes",
	Long: `Lists running testboxes. Active testboxes are Semaphore debug jobs in
state RUNNING named "sem-ai testbox" (the fixed name every 'testbox warmup'
uses) — this filters the running-jobs list down to just those, paging through
the full RUNNING job list first so an older testbox never drops off page 1.`,
	Example: `  sem-ai testbox list
  sem-ai testbox list --format table`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}

		c := client.New()

		allJobs, err := fetchRunningJobs(c)
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		matched := make([]testboxJobEntry, 0, len(allJobs))
		for _, job := range allJobs {
			if job.Metadata.Name == testboxJobName {
				matched = append(matched, job)
			}
		}

		projectNames := projectNamesByID(c, matched)

		testboxes := make([]map[string]any, 0, len(matched))
		for _, job := range matched {
			project := job.Spec.ProjectID
			if name, ok := projectNames[job.Spec.ProjectID]; ok && name != "" {
				project = name
			}
			testboxes = append(testboxes, map[string]any{
				"testbox_id": job.Metadata.ID,
				"age":        jobAge(job.Metadata.CreateTime),
				"machine":    job.Spec.Agent.Machine.Type,
				"project":    project,
			})
		}

		output.Result(map[string]any{"testboxes": testboxes})
		return nil
	},
}

// jobAge renders a job's create_time (a unix-seconds string, per the jobs
// API) as a human duration like "5m32s". Empty/unparseable input returns "".
// A future create_time (clock skew between client and server) would make
// time.Since negative — Duration.String() would render that as "-5s", which
// reads as nonsense for an "age" field, so it's clamped to "0s" instead.
func jobAge(createTime string) string {
	sec, err := strconv.ParseInt(createTime, 10, 64)
	if err != nil || sec <= 0 {
		return ""
	}
	d := time.Since(time.Unix(sec, 0))
	if d < 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}

// projectNamesByID resolves project_id -> project name for the given jobs
// with a single "list all projects" call (not one lookup per job). Best
// effort: a lookup failure just means callers fall back to showing the raw
// project_id, never an error surfaced to the user for a cosmetic field.
func projectNamesByID(c *client.Client, jobs []testboxJobEntry) map[string]string {
	names := make(map[string]string)
	if len(jobs) == 0 {
		return names
	}
	resp, err := c.List("projects")
	if err != nil || resp.StatusCode != 200 {
		return names
	}
	var projects []struct {
		Metadata struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(resp.Body, &projects); err != nil {
		return names
	}
	for _, p := range projects {
		if p.Metadata.ID != "" {
			names[p.Metadata.ID] = p.Metadata.Name
		}
	}
	return names
}

func init() {
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupProjectFlag, "project", "", "project name (auto-detected from git)")
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupMachineFlag, "machine", "f1-standard-2", "machine type (f1-standard-2, f1-standard-4, etc.)")
	testboxWarmupCmd.Flags().StringVar(&testboxWarmupOSImageFlag, "os-image", "ubuntu2204", "OS image (ubuntu2204, ubuntu2404)")
	testboxWarmupCmd.Flags().DurationVar(&testboxWarmupDurationFlag, "duration", 30*time.Minute, "max session duration")

	testboxRunCmd.Flags().StringVar(&testboxRunID, "id", "", "testbox ID (from warmup)")
	testboxSSHCmd.Flags().StringVar(&testboxSSHID, "id", "", "testbox ID")
	testboxStopCmd.Flags().StringVar(&testboxStopID, "id", "", "testbox ID")
	testboxStopCmd.Flags().DurationVar(&testboxStopTimeoutFlag, "timeout", 15*time.Second, "how long to wait for the job to confirm it stopped")

	testboxCmd.AddCommand(testboxWarmupCmd)
	testboxCmd.AddCommand(testboxRunCmd)
	testboxCmd.AddCommand(testboxSSHCmd)
	testboxCmd.AddCommand(testboxStopCmd)
	testboxCmd.AddCommand(testboxListCmd)
	rootCmd.AddCommand(testboxCmd)
}
