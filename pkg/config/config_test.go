package config

import (
	"testing"

	"github.com/spf13/viper"
)

func setFileContext(name, token, host string) {
	viper.Set("active-context", name)
	viper.Set("contexts."+name+".auth.token", token)
	viper.Set("contexts."+name+".host", host)
}

func TestLoad_FileContext(t *testing.T) {
	viper.Reset()
	t.Setenv(EnvToken, "")
	t.Setenv(EnvHost, "")
	setFileContext("acme", "filetok", "acme.semaphoreci.com")

	Load()

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

	Load()

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

	Load()

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

	Load()

	if GetToken() != "filetok" {
		t.Errorf("token = %q, want file value when env blank", GetToken())
	}
	if GetHost() != "acme.semaphoreci.com" {
		t.Errorf("host = %q, want file value when env blank", GetHost())
	}
}
