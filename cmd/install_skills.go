package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var installSkillsTargetFlag string

// Known agents and their default skill directories (relative to $HOME).
var agentSkillPaths = map[string]string{
	"claude": ".claude/skills",
	"codex":  ".agents/skills",
}

var installSkillsCmd = &cobra.Command{
	Use:   "install-skills <agent>",
	Short: "Install sem-agent skills for an AI agent",
	Long: fmt.Sprintf(`Copies sem-agent skill definitions to the agent's skill directory.

Supported agents and their default locations:
%s
Use --target to override the default path for any agent.

Skills follow the open Agent Skills standard (agentskills.io).
Includes a primary skill for orientation and sub-skills for specific workflows.`, agentList()),
	Args: cobra.MaximumNArgs(1),
	Example: `  sem-agent install-skills claude
  sem-agent install-skills codex
  sem-agent install-skills claude --target ./my-project/.claude/skills`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && installSkillsTargetFlag == "" {
			names := make([]string, 0, len(agentSkillPaths))
			for name := range agentSkillPaths {
				names = append(names, name)
			}
			output.Error("invalid_args",
				fmt.Sprintf("specify an agent: %s — or use --target for a custom path", strings.Join(names, ", ")),
				1)
			return fmt.Errorf("agent name required")
		}

		target := installSkillsTargetFlag
		if target == "" {
			agent := strings.ToLower(args[0])
			rel, ok := agentSkillPaths[agent]
			if !ok {
				names := make([]string, 0, len(agentSkillPaths))
				for name := range agentSkillPaths {
					names = append(names, name)
				}
				output.Error("unknown_agent",
					fmt.Sprintf("unknown agent %q — supported: %s — or use --target", agent, strings.Join(names, ", ")),
					1)
				return fmt.Errorf("unknown agent %q", agent)
			}
			home, err := homedir.Dir()
			if err != nil {
				output.Error("path_error", err.Error(), 1)
				return err
			}
			target = filepath.Join(home, rel)
		}

		sourceDir := findSkillsSource()
		if sourceDir == "" {
			output.Error("not_found", "could not find skills source directory", 1)
			return fmt.Errorf("skills source not found")
		}

		copied := 0
		err := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(sourceDir, path)
			dest := filepath.Join(target, rel)
			if d.IsDir() {
				return os.MkdirAll(dest, 0755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dest, data, 0644); err != nil {
				return err
			}
			copied++
			return nil
		})

		if err != nil {
			output.Error("install_error", err.Error(), 1)
			return err
		}

		agentName := ""
		if len(args) > 0 {
			agentName = args[0]
		}

		output.Result(map[string]any{
			"status": "installed",
			"agent":  agentName,
			"target": target,
			"files":  copied,
			"skills": []string{
				"semaphore-ci (primary — orientation + routing)",
				"debug-pipeline (diagnosing CI failures)",
				"testbox (run CI commands against local changes)",
				"deploy (promotions + deployment targets)",
				"test-intelligence (test results + flaky detection)",
				"manage-infra (secrets, notifications, agents, tasks)",
				"project-health (monitoring + trends)",
			},
		})
		return nil
	},
}

func agentList() string {
	var sb strings.Builder
	for name, path := range agentSkillPaths {
		sb.WriteString(fmt.Sprintf("  %-10s ~/%-s\n", name, path))
	}
	return sb.String()
}

func findSkillsSource() string {
	if info, err := os.Stat(".agents/skills"); err == nil && info.IsDir() {
		abs, _ := filepath.Abs(".agents/skills")
		return abs
	}

	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for _, candidate := range []string{
			filepath.Join(dir, ".agents", "skills"),
			filepath.Join(dir, "..", ".agents", "skills"),
		} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				abs, _ := filepath.Abs(candidate)
				return abs
			}
		}
	}

	home, _ := homedir.Dir()
	candidate := filepath.Join(home, "sem-agent", ".agents", "skills")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}

	return ""
}

func init() {
	installSkillsCmd.Flags().StringVar(&installSkillsTargetFlag, "target", "", "override default target directory")
	rootCmd.AddCommand(installSkillsCmd)
}
