package config

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/viper"
)

const (
	EnvToken = "SEMAPHORE_API_TOKEN"
	EnvHost  = "SEMAPHORE_HOST"
)

var cfg *Config

type Context struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

type Config struct {
	ActiveContext string
	Token         string
	Host          string
}

func Load() {
	cfg = &Config{}
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
