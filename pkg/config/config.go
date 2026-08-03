package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	EnvToken   = "SEMAPHORE_API_TOKEN"
	EnvHost    = "SEMAPHORE_HOST"
	EnvContext = "SEM_CONTEXT"
)

var cfg *Config

// explicitContext pins the invocation to a named context (--context flag).
// An explicit selector — flag or SEM_CONTEXT — resolves read-only and fully
// shadows the credential env vars and the shared active-context key, so
// concurrent invocations can't flip each other's context via ~/.sem.yaml.
var explicitContext string

type Context struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

type Config struct {
	ActiveContext string
	Token         string
	Host          string
}

func SetExplicitContext(name string) { explicitContext = name }

func Load() error {
	cfg = &Config{}

	name := explicitContext
	source := "--context"
	if name == "" {
		name = os.Getenv(EnvContext)
		source = EnvContext
	}
	if name != "" {
		token := viper.GetString(fmt.Sprintf("contexts.%s.auth.token", name))
		host := viper.GetString(fmt.Sprintf("contexts.%s.host", name))
		if token == "" && host == "" {
			return fmt.Errorf("context %q (from %s) not found in ~/.sem.yaml (available: %s)", name, source, availableContexts())
		}
		cfg.ActiveContext = name
		cfg.Token = token
		cfg.Host = host
		return nil
	}

	cfg.ActiveContext = viper.GetString("active-context")
	if cfg.ActiveContext != "" {
		cfg.Token = viper.GetString(fmt.Sprintf("contexts.%s.auth.token", cfg.ActiveContext))
		cfg.Host = viper.GetString(fmt.Sprintf("contexts.%s.host", cfg.ActiveContext))
	}

	if t := os.Getenv(EnvToken); t != "" {
		cfg.Token = t
	}
	if h := os.Getenv(EnvHost); h != "" {
		cfg.Host = h
		if cfg.ActiveContext == "" {
			cfg.ActiveContext = "env"
		}
	}
	return nil
}

// Write persists viper's current settings to the config file atomically: a
// temp file in the same directory, then rename(2).
//
// viper.WriteConfig truncates the target in place. A concurrent reader that
// opens ~/.sem.yaml mid-write sees a truncated or empty document, and viper
// keeps its prior in-memory state when a parse fails — so the reader carries
// on with stale (or absent) credentials and surfaces it as a 401 rather than
// a config error. rename(2) hands the reader the old file or the new one,
// never a half-written one.
//
// The bytes are identical to what viper.WriteConfig produced: it marshals the
// same viper.AllSettings() through the same yaml.Marshal.
func Write() error {
	path := viper.ConfigFileUsed()
	// rename(2) replaces the link, not its target, so write through to the
	// real file — otherwise a symlinked ~/.sem.yaml is silently detached from
	// wherever the user keeps it.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	// Only YAML gets the atomic path. viper picks its config file by scanning
	// SupportedExts in order and ignores SetConfigType while doing it, so a
	// stray ~/.sem.json wins over ~/.sem.yaml — writing YAML bytes into it
	// would leave an unparseable config. An empty path (no file ever read)
	// lands here too. Both fall back to viper's in-place write: not atomic,
	// but exactly the previous behaviour.
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
	default:
		return viper.WriteConfig()
	}

	data, err := yaml.Marshal(viper.AllSettings())
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// CreateTemp opens 0600, which is what this file needs — it holds API
	// tokens — and the rename carries that mode onto the target.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sem.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	// Flush before the rename so a crash leaves the whole old file or the
	// whole new one, rather than a renamed-but-empty one.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func availableContexts() string {
	contexts, err := ContextList()
	if err != nil || len(contexts) == 0 {
		return "none"
	}
	names := make([]string, 0, len(contexts))
	for _, c := range contexts {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

func GetActiveContext() string { return cfg.ActiveContext }
func GetToken() string         { return cfg.Token }
func GetHost() string          { return cfg.Host }

func ContextList() ([]Context, error) {
	raw := viper.GetStringMap("contexts")
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	contexts := make([]Context, 0, len(names))
	for _, name := range names {
		host := viper.GetString(fmt.Sprintf("contexts.%s.host", name))
		contexts = append(contexts, Context{Name: name, Host: host})
	}
	return contexts, nil
}

func IsConfigured() bool {
	return cfg != nil && cfg.Token != "" && cfg.Host != ""
}
