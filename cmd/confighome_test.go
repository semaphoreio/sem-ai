package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/spf13/viper"
)

// isolateHome points HOME at an empty temp dir for the duration of the test.
//
// Anything that reaches initConfig — every rootCmd.PersistentPreRunE, and so
// every executeCobra — reads ~/.sem.yaml and resolves a context from it. Left
// on the real home, such a test passes or fails according to the developer's
// config and environment (an exported SEM_CONTEXT alone is enough to fail it),
// and creates ~/.sem.yaml there when none exists. CI has neither, which is why
// the suite looks green.
func isolateHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	prevCache := homedir.DisableCache
	homedir.DisableCache = true
	homedir.Reset()
	t.Cleanup(func() {
		homedir.DisableCache = prevCache
		homedir.Reset()
		viper.Reset()
		// initConfig leaves the resolved selector in the config package. Left
		// set, every later test in this package resolves a context that only
		// existed in this test's temp home, and config.Load starts failing.
		contextFlag = ""
		config.SetExplicitContext("")
		config.IgnoreContextSelectors(false)
	})
	t.Setenv(config.EnvContext, "")

	return home
}

// isolateConfigHome is isolateHome plus a minimal ~/.sem.yaml: an active
// context named "isolated", followed by one context per extra name given.
func isolateConfigHome(t *testing.T, extra ...string) string {
	t.Helper()

	home := isolateHome(t)
	body := "active-context: isolated\ncontexts:\n" + contextBlock("isolated")
	for _, name := range extra {
		body += contextBlock(name)
	}
	if err := os.WriteFile(filepath.Join(home, ".sem.yaml"), []byte(body), 0600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	return home
}

func contextBlock(name string) string {
	return fmt.Sprintf("  %s:\n    host: %s.test\n    auth:\n      token: %stok\n", name, name, name)
}
