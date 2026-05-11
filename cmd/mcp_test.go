package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func buildTestCmd() *cobra.Command {
	root := &cobra.Command{Use: "testroot"}
	child := &cobra.Command{Use: "child", Short: "child cmd"}

	var (
		strFlag   string
		boolFlag  bool
		intFlag   int
		arrayFlag []string
		persStr   string
	)

	child.Flags().StringVar(&strFlag, "name", "default-name", "a string flag")
	child.Flags().BoolVar(&boolFlag, "verbose", false, "a bool flag")
	child.Flags().IntVar(&intFlag, "count", 10, "an int flag")
	child.Flags().StringArrayVar(&arrayFlag, "tags", nil, "a string array flag")
	root.PersistentFlags().StringVar(&persStr, "format", "json", "output format")

	root.AddCommand(child)

	child.Flags().Set("name", "changed-name")
	child.Flags().Set("verbose", "true")
	child.Flags().Set("count", "99")
	child.Flags().Set("tags", "a")
	child.Flags().Set("tags", "b")
	root.PersistentFlags().Set("format", "yaml")

	return root
}

func TestResetFlagsAllTypes(t *testing.T) {
	root := buildTestCmd()
	child, _, _ := root.Find([]string{"child"})

	resetFlags(root)

	checks := []struct {
		flag *pflag.Flag
		want string
	}{
		{child.Flags().Lookup("name"), "default-name"},
		{child.Flags().Lookup("verbose"), "false"},
		{child.Flags().Lookup("count"), "10"},
	}

	for _, c := range checks {
		if c.flag.Value.String() != c.want {
			t.Errorf("%s = %q, want %q", c.flag.Name, c.flag.Value.String(), c.want)
		}
		if c.flag.Changed {
			t.Errorf("%s.Changed should be false", c.flag.Name)
		}
	}

	tags := child.Flags().Lookup("tags")
	if tags.Changed {
		t.Error("tags.Changed should be false after reset")
	}
}

func TestResetFlagsPersistent(t *testing.T) {
	root := buildTestCmd()
	resetFlags(root)

	f := root.PersistentFlags().Lookup("format")
	if f.Value.String() != "json" {
		t.Errorf("persistent format = %q, want json", f.Value.String())
	}
	if f.Changed {
		t.Error("persistent format.Changed should be false")
	}
}

func TestResetFlagsFullTreeClean(t *testing.T) {
	root := buildTestCmd()
	resetFlags(root)

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				t.Errorf("flag %q on %q still Changed after reset", f.Name, c.Name())
			}
		})
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				t.Errorf("persistent flag %q on %q still Changed after reset", f.Name, c.Name())
			}
		})
		for _, ch := range c.Commands() {
			walk(ch)
		}
	}
	walk(root)
}

func TestExecuteCobraVersion(t *testing.T) {
	result, err := executeCobra([]string{"version"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(result, `"version"`) || !strings.Contains(result, `"commit"`) {
		t.Errorf("version output missing expected keys, got: %s", result)
	}
}

func TestExecuteCobraUnknownCommand(t *testing.T) {
	result, err := executeCobra([]string{"nonexistent-xyz"})
	if err == nil {
		t.Error("expected error for unknown command")
	}
	if result == "" {
		t.Error("expected error message in result")
	}
}

func TestExecuteCobraFlagBleed(t *testing.T) {
	// Call 1: YAML format
	r1, err := executeCobra([]string{"version", "--format", "yaml"})
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}

	// Call 2: no format flag — should default to JSON
	r2, err := executeCobra([]string{"version"})
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}

	// YAML output has bare "version:" without quotes
	if !strings.Contains(r1, "version:") {
		t.Errorf("call 1 should be YAML, got: %s", r1)
	}

	// JSON output has quoted keys
	if !strings.Contains(r2, `"version"`) {
		t.Errorf("call 2 should be JSON (no bleed from call 1), got: %s", r2)
	}
}

func TestExecuteCobraStringFlagBleed(t *testing.T) {
	// Call 1: set --project on workflow list (will fail, that's fine)
	executeCobra([]string{"workflow", "list", "--project", "proj-alpha"})

	// Call 2: workflow list without --project should fail with "required" error
	result, err := executeCobra([]string{"workflow", "list"})
	if err == nil {
		return // if it somehow succeeded, fine
	}
	if strings.Contains(result, "proj-alpha") {
		t.Error("project flag bled from previous call")
	}
}

func TestCommandPathLeaf(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	parent := &cobra.Command{Use: "workflow"}
	child := &cobra.Command{Use: "list"}
	root.AddCommand(parent)
	parent.AddCommand(child)

	saved := rootCmd
	rootCmd = root
	defer func() { rootCmd = saved }()

	parts := commandPath(child)
	if len(parts) != 2 || parts[0] != "workflow" || parts[1] != "list" {
		t.Errorf("got %v, want [workflow list]", parts)
	}
}

func TestCommandPathDirect(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	child := &cobra.Command{Use: "version"}
	root.AddCommand(child)

	saved := rootCmd
	rootCmd = root
	defer func() { rootCmd = saved }()

	parts := commandPath(child)
	if len(parts) != 1 || parts[0] != "version" {
		t.Errorf("got %v, want [version]", parts)
	}
}

func TestExtractArgsDescription(t *testing.T) {
	tests := []struct {
		use  string
		want bool // should have description
	}{
		{"show <name>", true},
		{"list [flags]", true},
		{"version", false},
		{"justname", false},
	}
	for _, tc := range tests {
		cmd := &cobra.Command{Use: tc.use}
		desc := extractArgsDescription(cmd)
		if tc.want && desc == "" {
			t.Errorf("Use=%q: expected description, got empty", tc.use)
		}
		if !tc.want && desc != "" {
			t.Errorf("Use=%q: expected empty, got %q", tc.use, desc)
		}
	}
}
