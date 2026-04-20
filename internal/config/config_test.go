package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsNonPositiveUsageSnapshotInterval(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "test-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("USAGE_SNAPSHOT_INTERVAL_MINUTES", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "USAGE_SNAPSHOT_INTERVAL_MINUTES") {
		t.Fatalf("Load() error = %q, want interval validation message", err.Error())
	}
}
