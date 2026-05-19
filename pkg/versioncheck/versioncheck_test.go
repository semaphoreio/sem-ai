package versioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func swapEndpoint(t *testing.T, url string) {
	t.Helper()
	old := Endpoint
	Endpoint = url
	t.Cleanup(func() { Endpoint = old })
}

func TestLatest_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Errorf("missing User-Agent header")
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q, want application/vnd.github+json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v0.4.1",
			"published_at": "2026-05-18T09:30:00Z",
		})
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.Version != "0.4.1" {
		t.Errorf("Version = %q, want 0.4.1", rel.Version)
	}
	if rel.PublishedAt.IsZero() {
		t.Errorf("PublishedAt is zero")
	}
}

func TestLatest_StripsLeadingV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v1.2.3",
			"published_at": "2026-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3 (no leading v)", rel.Version)
	}
}

func TestLatest_HTTP403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	_, err := Latest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q should mention 403", err)
	}
}

func TestLatest_HTTP5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	_, err := Latest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLatest_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	_, err := Latest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLatest_MissingTagName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"published_at": "2026-05-18T09:30:00Z",
		})
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	_, err := Latest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tag_name") {
		t.Errorf("error %q should mention tag_name", err)
	}
}

func TestLatest_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // stall until client disconnects
	}))
	defer srv.Close()
	swapEndpoint(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Latest(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Latest took %v; should have respected ctx (≤100ms slack)", elapsed)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		current   string
		latest    string
		wantNewer bool
		wantErr   error
	}{
		{"dev", "0.0.1", false, nil},
		{"DEV", "0.0.1", false, nil},
		{"0.4.1", "dev", false, nil},
		{"0.4.1", "0.4.1", false, nil},
		{"0.3.0", "0.4.1", true, nil},
		{"0.4.1", "0.3.0", false, nil},
		{"v0.3.0", "v0.4.1", true, nil},
		{"0.4.0", "0.4.1", true, nil},
		{"0.4.1", "1.0.0", true, nil},
		{"0.4.1-rc1", "0.4.1", true, nil},
		{"0.4.1", "0.4.1-rc1", false, nil},
		{"0.4.1-rc1", "0.4.1-rc2", true, nil},
		{"0.4.1-rc2", "0.4.1-rc1", false, nil},
		{"invalid", "0.4.1", false, ErrInvalidSemver},
		{"0.4.1", "invalid", false, ErrInvalidSemver},
		{"0.4", "0.4.1", false, ErrInvalidSemver},
		{"0.4.1.5", "0.4.2", false, ErrInvalidSemver},
		{"-1.0.0", "0.4.1", false, ErrInvalidSemver},
	}

	for _, tc := range cases {
		name := tc.current + "_vs_" + tc.latest
		t.Run(name, func(t *testing.T) {
			got, err := Compare(tc.current, tc.latest)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantNewer {
				t.Errorf("Compare(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.wantNewer)
			}
		})
	}
}

func TestEnvOptOut(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"False", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"anything", true},
	}

	for _, tc := range cases {
		t.Run("v="+tc.value, func(t *testing.T) {
			t.Setenv("SEM_AI_NO_UPDATE_CHECK", tc.value)
			if got := EnvOptOut(); got != tc.want {
				t.Errorf("EnvOptOut() = %v, want %v (env=%q)", got, tc.want, tc.value)
			}
		})
	}
}
