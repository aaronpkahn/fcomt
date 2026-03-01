package scheduler

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
)

func TestIntervalForProfile(t *testing.T) {
	m := config.MonitoringConfig{
		LocalProbeIntervalS:   60,
		ServerProbeIntervalMS: 500,
	}

	if got := IntervalForProfile(config.ProfileLocalOnly, m).Seconds(); got != 60 {
		t.Fatalf("expected local interval 60s, got %.0fs", got)
	}
	if got := IntervalForProfile(config.ProfileServerAssisted, m).Milliseconds(); got != 500 {
		t.Fatalf("expected server interval 500ms, got %dms", got)
	}
}

func TestBuildTargetsForProfiles(t *testing.T) {
	m := config.MonitoringConfig{
		Profile: config.ProfileLocalOnly,
		Destinations: []config.TargetConfig{
			{Name: "dest", Host: "8.8.8.8", Port: 53},
		},
		Peers: []config.TargetConfig{
			{Name: "peer", Host: "10.0.0.2", Port: 23999},
		},
	}

	targets := BuildTargets(m)
	if len(targets) != 1 {
		t.Fatalf("expected only destination targets in local mode, got %d", len(targets))
	}

	m.Profile = config.ProfileServerAssisted
	targets = BuildTargets(m)
	if len(targets) != 2 {
		t.Fatalf("expected destination and peer targets in server mode, got %d", len(targets))
	}
}

func TestEvaluationIntervalForProfile(t *testing.T) {
	if got := EvaluationIntervalForProfile(config.ProfileLocalOnly); got != time.Minute {
		t.Fatalf("expected local evaluation interval 1m, got %s", got)
	}
	if got := EvaluationIntervalForProfile(config.ProfileServerAssisted); got != 10*time.Second {
		t.Fatalf("expected server evaluation interval 10s, got %s", got)
	}
}
