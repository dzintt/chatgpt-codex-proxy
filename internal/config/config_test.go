package config

import (
	"testing"
)

func TestLoadRequiresProxyAPIKey(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want missing PROXY_API_KEY error")
	}
}

func TestLoadUsesDefaultPortAndDataDir(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("Load() listen addr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.DataDir == "" {
		t.Fatal("Load() data dir = empty, want resolved data path")
	}
}

func TestLoadAcceptsPortOverride(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("PORT", "9090")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("Load() listen addr = %q, want :9090", cfg.ListenAddr)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("PORT", "not-a-port")
	t.Chdir(t.TempDir())

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid PORT error")
	}
}
