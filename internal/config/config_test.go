package config

import (
	"os"
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

func TestLoadBuildsListenAddrAndDataDir(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")

	tests := []struct {
		name       string
		env        map[string]string
		wantListen string
		wantData   string
	}{
		{
			name:       "defaults",
			wantListen: ":8080",
			wantData:   "data",
		},
		{
			name: "data dir override",
			env: map[string]string{
				"DATA_DIR": "custom-data",
			},
			wantListen: ":8080",
			wantData:   "custom-data",
		},
		{
			name: "port override",
			env: map[string]string{
				"PORT": "9090",
			},
			wantListen: ":9090",
			wantData:   "data",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.ListenAddr != tc.wantListen {
				t.Fatalf("Load() listen addr = %q, want %q", cfg.ListenAddr, tc.wantListen)
			}
			wantDataDir := filepath.Join(cwd, tc.wantData)
			if cfg.DataDir != wantDataDir {
				t.Fatalf("Load() data dir = %q, want %q", cfg.DataDir, wantDataDir)
			}
		})
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

func TestLoadParsesDebugLogPayloads(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("DEBUG_LOG_PAYLOADS", "true")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DebugLogPayloads {
		t.Fatal("Load() debug log payloads = false, want true")
	}
}

func TestLoadRejectsInvalidDebugLogPayloads(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("DEBUG_LOG_PAYLOADS", "definitely-not-bool")
	t.Chdir(t.TempDir())

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid DEBUG_LOG_PAYLOADS error")
	}
}

func TestLoadRejectsInvalidDotEnv(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("INVALID LINE"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid .env error")
	}
}
