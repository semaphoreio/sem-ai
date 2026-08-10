package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func setFileContext(name, token, host string) {
	viper.Set("active-context", name)
	viper.Set("contexts."+name+".auth.token", token)
	viper.Set("contexts."+name+".host", host)
}

func addContext(name, token, host string) {
	viper.Set("contexts."+name+".auth.token", token)
	viper.Set("contexts."+name+".host", host)
}

func pinContext(t *testing.T, name string) {
	t.Helper()
	SetExplicitContext(name)
	t.Cleanup(func() { SetExplicitContext("") })
}

func TestLoad_FileContext(t *testing.T) {
	viper.Reset()
	t.Setenv(EnvToken, "")
	t.Setenv(EnvHost, "")
	setFileContext("acme", "filetok", "acme.semaphoreci.com")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetToken() != "filetok" {
		t.Errorf("token = %q, want %q", GetToken(), "filetok")
	}
	if GetHost() != "acme.semaphoreci.com" {
		t.Errorf("host = %q, want %q", GetHost(), "acme.semaphoreci.com")
	}
	if !IsConfigured() {
		t.Error("IsConfigured() = false, want true")
	}
}

func TestLoad_EnvOnly(t *testing.T) {
	viper.Reset()
	t.Setenv(EnvToken, "envtok")
	t.Setenv(EnvHost, "env.semaphoreci.com")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetToken() != "envtok" {
		t.Errorf("token = %q, want %q", GetToken(), "envtok")
	}
	if GetHost() != "env.semaphoreci.com" {
		t.Errorf("host = %q, want %q", GetHost(), "env.semaphoreci.com")
	}
	if GetActiveContext() != "env" {
		t.Errorf("active context = %q, want %q", GetActiveContext(), "env")
	}
	if !IsConfigured() {
		t.Error("IsConfigured() = false, want true")
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "filetok", "acme.semaphoreci.com")
	t.Setenv(EnvToken, "envtok")
	t.Setenv(EnvHost, "env.semaphoreci.com")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetToken() != "envtok" {
		t.Errorf("token = %q, want env to win, got %q", GetToken(), "envtok")
	}
	if GetHost() != "env.semaphoreci.com" {
		t.Errorf("host = %q, want env to win, got %q", GetHost(), "env.semaphoreci.com")
	}
	if GetActiveContext() != "acme" {
		t.Errorf("active context = %q, want file context preserved", GetActiveContext())
	}
}

func TestLoad_EmptyEnvFallsBackToFile(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "filetok", "acme.semaphoreci.com")
	t.Setenv(EnvToken, "")
	t.Setenv(EnvHost, "")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetToken() != "filetok" {
		t.Errorf("token = %q, want file value when env blank", GetToken())
	}
	if GetHost() != "acme.semaphoreci.com" {
		t.Errorf("host = %q, want file value when env blank", GetHost())
	}
}

func TestLoad_ExplicitContextIgnoresActiveContext(t *testing.T) {
	viper.Reset()
	t.Setenv(EnvToken, "")
	t.Setenv(EnvHost, "")
	setFileContext("acme", "acmetok", "acme.semaphoreci.com")
	addContext("sxmoon", "sxtok", "sxmoon.semaphoreci.com")
	pinContext(t, "sxmoon")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetActiveContext() != "sxmoon" {
		t.Errorf("active context = %q, want %q", GetActiveContext(), "sxmoon")
	}
	if GetToken() != "sxtok" {
		t.Errorf("token = %q, want %q", GetToken(), "sxtok")
	}
	if GetHost() != "sxmoon.semaphoreci.com" {
		t.Errorf("host = %q, want %q", GetHost(), "sxmoon.semaphoreci.com")
	}
}

func TestLoad_ExplicitContextShadowsCredentialEnv(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "acmetok", "acme.semaphoreci.com")
	addContext("sxmoon", "sxtok", "sxmoon.semaphoreci.com")
	t.Setenv(EnvToken, "envtok")
	t.Setenv(EnvHost, "env.semaphoreci.com")
	pinContext(t, "sxmoon")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetToken() != "sxtok" {
		t.Errorf("token = %q, want explicit context to shadow env", GetToken())
	}
	if GetHost() != "sxmoon.semaphoreci.com" {
		t.Errorf("host = %q, want explicit context to shadow env", GetHost())
	}
}

func TestLoad_EnvContextSelector(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "acmetok", "acme.semaphoreci.com")
	addContext("sxmoon", "sxtok", "sxmoon.semaphoreci.com")
	t.Setenv(EnvContext, "sxmoon")
	t.Setenv(EnvToken, "")
	t.Setenv(EnvHost, "")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetActiveContext() != "sxmoon" {
		t.Errorf("active context = %q, want SEM_CONTEXT to select", GetActiveContext())
	}
	if GetToken() != "sxtok" {
		t.Errorf("token = %q, want %q", GetToken(), "sxtok")
	}
}

func TestLoad_FlagBeatsEnvContext(t *testing.T) {
	viper.Reset()
	addContext("flagctx", "flagtok", "flag.semaphoreci.com")
	addContext("envctx", "envctxtok", "envctx.semaphoreci.com")
	t.Setenv(EnvContext, "envctx")
	pinContext(t, "flagctx")

	if err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if GetActiveContext() != "flagctx" {
		t.Errorf("active context = %q, want flag to beat SEM_CONTEXT", GetActiveContext())
	}
	if GetToken() != "flagtok" {
		t.Errorf("token = %q, want %q", GetToken(), "flagtok")
	}
}

func TestLoad_UnknownExplicitContextErrors(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "acmetok", "acme.semaphoreci.com")
	pinContext(t, "nope")

	err := Load()
	if err == nil {
		t.Fatal("Load() = nil error, want unknown-context error")
	}
	if !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "acme") {
		t.Errorf("error %q should name the missing context and list available ones", err)
	}
}

func TestLoad_UnknownEnvContextErrors(t *testing.T) {
	viper.Reset()
	setFileContext("acme", "acmetok", "acme.semaphoreci.com")
	t.Setenv(EnvContext, "ghost")

	err := Load()
	if err == nil {
		t.Fatal("Load() = nil error, want unknown-context error")
	}
	if !strings.Contains(err.Error(), EnvContext) {
		t.Errorf("error %q should say the selector came from %s", err, EnvContext)
	}
}

func writableConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".sem.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	viper.Reset()
	viper.SetConfigType("yaml")
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	return path
}

func TestWrite_RoundTrips(t *testing.T) {
	path := writableConfig(t, "active-context: acme\ncontexts:\n  acme:\n    host: acme.semaphoreci.com\n    auth:\n      token: acmetok\n")

	viper.Set("active-context", "beta")
	addContext("beta", "betatok", "beta.semaphoreci.com")
	if err := Write(); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	viper.Reset()
	viper.SetConfigType("yaml")
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if got := viper.GetString("active-context"); got != "beta" {
		t.Errorf("active-context = %q, want %q", got, "beta")
	}
	if got := viper.GetString("contexts.acme.auth.token"); got != "acmetok" {
		t.Errorf("pre-existing context lost: token = %q, want %q", got, "acmetok")
	}
	if got := viper.GetString("contexts.beta.host"); got != "beta.semaphoreci.com" {
		t.Errorf("new context host = %q, want %q", got, "beta.semaphoreci.com")
	}
}

func TestWrite_LeavesNoTempFiles(t *testing.T) {
	path := writableConfig(t, "active-context: acme\ncontexts:\n  acme:\n    host: acme.semaphoreci.com\n    auth:\n      token: acmetok\n")

	if err := Write(); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ".sem.yaml" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contents = %v, want only .sem.yaml", names)
	}
}

func TestWrite_KeepsFileReadableThroughout(t *testing.T) {
	path := writableConfig(t, "active-context: acme\ncontexts:\n  acme:\n    host: acme.semaphoreci.com\n    auth:\n      token: acmetok\n")

	// A reader racing the writer must always parse a complete document —
	// rename(2) swaps the inode, so it sees either the old file or the new
	// one. viper.WriteConfig truncates in place and would expose an empty
	// or half-written file to this loop. The reader runs until the writer
	// stops rather than for a fixed count, so it covers the whole write
	// window regardless of how fast Write() gets.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			var parsed map[string]any
			if err := yaml.Unmarshal(data, &parsed); err != nil {
				t.Errorf("parse: %v", err)
				return
			}
			if parsed["contexts"] == nil {
				t.Errorf("observed document without contexts: %q", data)
				return
			}
		}
	}()

	for i := 0; i < 50; i++ {
		addContext(fmt.Sprintf("ctx%d", i), "tok", "host.semaphoreci.com")
		if err := Write(); err != nil {
			close(stop)
			<-done
			t.Fatalf("Write() error: %v", err)
		}
	}
	close(stop)
	<-done
}

func TestWrite_Permissions(t *testing.T) {
	path := writableConfig(t, "active-context: acme\ncontexts:\n  acme:\n    host: acme.semaphoreci.com\n    auth:\n      token: acmetok\n")

	if err := Write(); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("mode = %o, want 600 — the file holds API tokens", perm)
	}
}

func TestWrite_PreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles-sem.yaml")
	if err := os.WriteFile(real, []byte("active-context: acme\ncontexts:\n  acme:\n    host: acme.semaphoreci.com\n    auth:\n      token: acmetok\n"), 0600); err != nil {
		t.Fatalf("seed real config: %v", err)
	}
	link := filepath.Join(dir, ".sem.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	viper.Reset()
	viper.SetConfigType("yaml")
	viper.SetConfigFile(link)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("read through symlink: %v", err)
	}

	viper.Set("active-context", "beta")
	addContext("beta", "betatok", "beta.semaphoreci.com")
	if err := Write(); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by a regular file — the user's real config is now orphaned")
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read real config: %v", err)
	}
	if !strings.Contains(string(data), "beta") {
		t.Errorf("wrote past the symlink target; %s = %q", real, data)
	}
}

// viper picks its config file by scanning SupportedExts in order and ignores
// SetConfigType, so a stray ~/.sem.json wins over ~/.sem.yaml. Write must not
// stamp YAML bytes into it.
func TestWrite_NonYAMLTargetStaysParseable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sem.json")
	if err := os.WriteFile(path, []byte(`{"active-context":"acme","contexts":{"acme":{"host":"acme.semaphoreci.com","auth":{"token":"acmetok"}}}}`), 0600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	viper.Reset()
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("read seeded config: %v", err)
	}

	viper.Set("active-context", "beta")
	if err := Write(); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	viper.Reset()
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("config unparseable after Write: %v", err)
	}
	if got := viper.GetString("active-context"); got != "beta" {
		t.Errorf("active-context = %q, want %q", got, "beta")
	}
}
