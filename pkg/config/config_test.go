package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
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
