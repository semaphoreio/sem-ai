package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// viper.Set (connect/signin/context switch) writes to viper's override
// registry, which ReadInConfig never clears. Without the Reset in initConfig,
// one mutating call in the long-lived MCP server pins its values over every
// later call's re-read of the file.
func TestInitConfigDropsStaleOverrides(t *testing.T) {
	home := isolateHome(t)

	seed := "active-context: fromfile\ncontexts:\n  fromfile:\n    host: fromfile.semaphoreci.com\n    auth:\n      token: filetok\n"
	if err := os.WriteFile(filepath.Join(home, ".sem.yaml"), []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	viper.Set("active-context", "stale-override")

	if err := initConfig(); err != nil {
		t.Fatalf("initConfig: %v", err)
	}
	if got := viper.GetString("active-context"); got != "fromfile" {
		t.Errorf("active-context = %q, want %q — stale override survived initConfig", got, "fromfile")
	}
}

func TestInitConfigReportsMalformedConfig(t *testing.T) {
	home := isolateHome(t)

	if err := os.WriteFile(filepath.Join(home, ".sem.yaml"), []byte("contexts: [unclosed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	err := initConfig()
	if err == nil {
		t.Fatal("initConfig must surface a malformed config instead of proceeding with empty credentials")
	}
	if !strings.Contains(err.Error(), "could not read config") {
		t.Errorf("error = %q, want it to name the config read failure", err)
	}
}
