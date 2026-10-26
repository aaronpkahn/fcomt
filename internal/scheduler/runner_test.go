package scheduler

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/storage"
)

func TestIntervalForProfile(t *testing.T) {
	m := config.MonitoringConfig{LocalProbeIntervalS: 30, ServerProbeIntervalMS: 500}
	if got := IntervalForProfile(config.ProfileLocalOnly, m).Seconds(); got != 30 {
		t.Fatalf("expected 30s, got %.0fs", got)
	}
	if got := IntervalForProfile(config.ProfileServerAssisted, m).Milliseconds(); got != 500 {
		t.Fatalf("expected 500ms, got %dms", got)
	}
}

func TestBuildTargetsForProfiles(t *testing.T) {
	m := config.MonitoringConfig{
		Profile:      config.ProfileLocalOnly,
		Destinations: []config.TargetConfig{{Name: "dest", Host: "8.8.8.8", Port: 53}},
		Peers:        []config.TargetConfig{{Name: "peer", Host: "10.0.0.2", Port: 23999}},
	}
	if n := len(BuildTargets(m)); n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
	m.Profile = config.ProfileServerAssisted
	if n := len(BuildTargets(m)); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

type fakeProber struct{ up map[string]bool }

func (f *fakeProber) Probe(_ context.Context, t probe.Target) probe.Result {
	return probe.Result{Target: t.Name, Kind: t.Kind, Success: f.up[t.Name], Latency: time.Millisecond}
}

func TestRoundsOutagesAndGaps(t *testing.T) {
	cfg := config.Default()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp := &fakeProber{up: map[string]bool{"google-dns": true, "cloudflare-dns": true, "aws-us-east-1": true, "gateway": true}}
	r := New(cfg, fp, metrics.NewCollector(time.Hour), incident.NewEvaluator(cfg.SLA, nil), incident.NewStore(100), log.New(io.Discard, "", 0))
	r.SetDB(db)
	r.SetGateway("192.168.1.1", 0)
	targets := r.Targets()
	ctx := context.Background()
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	r.RunRound(ctx, targets, t0)
	for _, n := range []string{"google-dns", "cloudflare-dns", "aws-us-east-1"} {
		fp.up[n] = false
	}
	r.RunRound(ctx, targets, t0.Add(30*time.Second))
	r.RunRound(ctx, targets, t0.Add(60*time.Second))
	fp.up["google-dns"] = true
	r.RunRound(ctx, targets, t0.Add(90*time.Second))

	outs, _ := db.Outages(t0, t0.Add(time.Hour))
	if len(outs) != 1 || outs[0].End == nil || outs[0].Cause != "isp" || outs[0].End.Sub(outs[0].Start) != time.Minute {
		t.Fatalf("unexpected outages %+v", outs)
	}
	// Machine sleeps for 10 minutes.
	r.RunRound(ctx, targets, t0.Add(11*time.Minute))
	gaps, _ := db.Gaps(t0, t0.Add(time.Hour))
	if len(gaps) != 1 || gaps[0].Reason != "sleep" {
		t.Fatalf("expected sleep gap, got %+v", gaps)
	}
	// Restart 20 minutes later.
	r2 := New(cfg, fp, metrics.NewCollector(time.Hour), incident.NewEvaluator(cfg.SLA, nil), incident.NewStore(100), log.New(io.Discard, "", 0))
	r2.SetDB(db)
	r2.Resume(t0.Add(31 * time.Minute))
	gaps, _ = db.Gaps(t0, t0.Add(time.Hour))
	if len(gaps) != 2 || gaps[1].Reason != "restart" {
		t.Fatalf("expected restart gap, got %+v", gaps)
	}
}
