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

func TestFastPollDefaultsAndValidation(t *testing.T) {
	fp := Default().Monitoring.FastPoll
	if !fp.Enabled || fp.Interval().Seconds() != 2 || fp.ProbeTimeout().Seconds() != 1 || fp.Tail().Seconds() != 60 ||
		fp.MaxFast().Minutes() != 10 || fp.SlowInterval().Seconds() != 10 || len(fp.Targets) != 2 {
		t.Fatalf("defaults %+v", fp)
	}
	bad := Default()
	bad.Monitoring.FastPoll.ProbeTimeoutMS = 3000 // > interval
	if bad.Validate() == nil {
		t.Fatal("timeout > interval must be rejected")
	}
	bad = Default()
	bad.Monitoring.FastPoll.SlowIntervalMS = 1000
	if bad.Validate() == nil {
		t.Fatal("slow < fast must be rejected")
	}
	off := Default()
	off.Monitoring.FastPoll = FastPollConfig{Enabled: false}
	if err := off.Validate(); err != nil {
		t.Fatalf("disabled fast poll needs no settings: %v", err)
	}
	// Partial YAML keeps the other defaults.
	path := filepath.Join(t.TempDir(), "c.yaml")
	_ = os.WriteFile(path, []byte("monitoring:\n  fast_poll:\n    tail_s: 90\n"), 0o644)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Monitoring.FastPoll.TailS != 90 || cfg.Monitoring.FastPoll.IntervalMS != 2000 || cfg.Monitoring.LocalProbeIntervalS != 30 {
		t.Fatalf("merge %+v", cfg.Monitoring.FastPoll)
	}
}
