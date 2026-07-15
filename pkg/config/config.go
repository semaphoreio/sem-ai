package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/viper"
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
