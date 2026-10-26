package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if cfg.Monitoring.Profile != ProfileLocalOnly {
		t.Fatalf("expected local_only profile, got %s", cfg.Monitoring.Profile)
	}
	if got := cfg.Monitoring.ProbeInterval().Seconds(); got != 30 {
		t.Fatalf("expected 30s local interval, got %.0fs", got)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
monitoring:
  profile: server_assisted
  local_probe_interval_s: 60
  server_probe_interval_ms: 250
  probe_timeout_s: 2
  destinations:
    - name: google-dns
      host: 8.8.8.8
      port: 53
api:
  listen: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg.Monitoring.Profile != ProfileServerAssisted {
		t.Fatalf("expected profile server_assisted, got %s", cfg.Monitoring.Profile)
	}
	if got := cfg.Monitoring.ProbeInterval().Milliseconds(); got != 250 {
		t.Fatalf("expected 250ms probe interval, got %dms", got)
	}
}
