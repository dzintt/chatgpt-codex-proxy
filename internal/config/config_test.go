package config

import (
	"path/filepath"
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
	cwd := t.TempDir()
	t.Chdir(cwd)

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
	if cfg.DataDir != filepath.Join(cwd, "data") {
		t.Fatalf("Load() data dir = %q, want %q", cfg.DataDir, filepath.Join(cwd, "data"))
	}
}

func TestLoadHonorsDataDirOverride(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("DATA_DIR", "custom-data")
	cwd := t.TempDir()
	t.Chdir(cwd)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DataDir != filepath.Join(cwd, "custom-data") {
		t.Fatalf("Load() data dir = %q, want %q", cfg.DataDir, filepath.Join(cwd, "custom-data"))
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
