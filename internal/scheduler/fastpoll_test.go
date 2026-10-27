package scheduler

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/storage"
)

func newFP() *FastPoll { return NewFastPoll(30*time.Second, config.DefaultFastPoll()) }

func TestFastPollEnterTailExit(t *testing.T) {
	f := newFP()
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	if f.Mode() != probe.ModeNormal || f.Interval() != 30*time.Second {
		t.Fatalf("start %s %s", f.Mode(), f.Interval())
	}
	if _, ok := f.Observe(t0, false); ok || f.Mode() != probe.ModeNormal {
		t.Fatal("success in normal must not change mode")
	}
	tr, ok := f.Observe(t0, true)
	if !ok || tr.To != probe.ModeFast || f.Interval() != 2*time.Second {
		t.Fatalf("first failure should enter fast: %+v %s", tr, f.Interval())
	}
	now := t0
	for i := 0; i < 10; i++ { // still failing
		now = now.Add(2 * time.Second)
		if _, ok := f.Observe(now, true); ok {
			t.Fatal("unexpected transition while failing")
		}
	}
	rec := now.Add(2 * time.Second)
	f.Observe(rec, false) // first success starts the 60s tail
	for now = rec; now.Sub(rec) < 58*time.Second; {
		now = now.Add(2 * time.Second)
		if _, ok := f.Observe(now, false); ok {
			t.Fatalf("left fast after only %s of tail", now.Sub(rec))
		}
	}
	now = now.Add(2 * time.Second)
	tr, ok = f.Observe(now, false)
	if !ok || tr.To != probe.ModeNormal || now.Sub(rec) != 60*time.Second || f.Interval() != 30*time.Second {
		t.Fatalf("expected exit to normal at 60s tail: %+v after %s", tr, now.Sub(rec))
	}
}

func TestFastPollFlapRestartsTail(t *testing.T) {
	f := newFP()
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	f.Observe(t0, true)
	f.Observe(t0.Add(2*time.Second), false)  // tail starts
	f.Observe(t0.Add(50*time.Second), false) // 48s into tail
	f.Observe(t0.Add(52*time.Second), true)  // flap: tail resets
	f.Observe(t0.Add(54*time.Second), false) // tail restarts here
	if _, ok := f.Observe(t0.Add(100*time.Second), false); ok {
		t.Fatal("tail should have restarted at 54s")
	}
	if tr, ok := f.Observe(t0.Add(114*time.Second), false); !ok || tr.To != probe.ModeNormal {
		t.Fatalf("expected normal 60s after 54s: %+v", tr)
	}
}

func TestFastPollCapThenSlowThenRecover(t *testing.T) {
	f := newFP()
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	f.Observe(t0, true)
	now := t0
	for now.Sub(t0) < 10*time.Minute-2*time.Second {
		now = now.Add(2 * time.Second)
		f.Observe(now, true)
		if f.Mode() != probe.ModeFast {
			t.Fatalf("capped early at %s", now.Sub(t0))
		}
	}
	now = now.Add(2 * time.Second)
	tr, ok := f.Observe(now, true)
	if !ok || tr.To != probe.ModeSlow || f.Interval() != 10*time.Second || tr.Window != 10*time.Minute {
		t.Fatalf("expected slow at 10m cap: %+v %s", tr, f.Interval())
	}
	for i := 0; i < 30; i++ { // 5 more minutes down at 10s
		now = now.Add(10 * time.Second)
		if _, ok := f.Observe(now, true); ok {
			t.Fatal("unexpected transition while still down after cap")
		}
	}
	rec := now.Add(10 * time.Second)
	f.Observe(rec, false)
	if f.Mode() != probe.ModeSlow {
		t.Fatal("tail after cap stays at slow cadence")
	}
	if _, ok := f.Observe(rec.Add(50*time.Second), false); ok {
		t.Fatal("tail not finished")
	}
	if tr, ok := f.Observe(rec.Add(60*time.Second), false); !ok || tr.To != probe.ModeNormal {
		t.Fatalf("expected normal after tail: %+v", tr)
	}
}

func TestFastPollDisabledAndServerProfile(t *testing.T) {
	c := config.DefaultFastPoll()
	c.Enabled = false
	f := NewFastPoll(30*time.Second, c)
	f.Observe(time.Now(), true)
	if f.Mode() != probe.ModeNormal {
		t.Fatal("disabled fast poll must stay normal")
	}
	// Normal cadence already faster than 2s (server_assisted 500ms).
	f = NewFastPoll(500*time.Millisecond, config.DefaultFastPoll())
	f.Observe(time.Now(), true)
	if f.Mode() != probe.ModeNormal {
		t.Fatal("fast poll should be off when normal interval <= fast interval")
	}
}

func TestFastTargetsAndRoundFailed(t *testing.T) {
	all := []probe.Target{
		{Name: "google-dns", Kind: probe.KindDestination}, {Name: "cloudflare-dns", Kind: probe.KindDestination},
		{Name: "aws-us-east-1", Kind: probe.KindDestination}, {Name: "gateway", Kind: probe.KindGateway},
	}
	got := FastTargets(all, []string{"cloudflare-dns", "google-dns"})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, x := range got {
		if x.Name == "aws-us-east-1" {
			t.Fatal("aws must be skipped in fast mode")
		}
	}
	if len(FastTargets(all, []string{"nope"})) != 4 {
		t.Fatal("unknown names should fall back to all targets")
	}
	if RoundFailed([]probe.Result{{Kind: probe.KindGateway, Success: false}, {Kind: probe.KindDestination, Success: true}}) {
		t.Fatal("gateway-only failure must not trigger fast mode")
	}
	if !RoundFailed([]probe.Result{{Kind: probe.KindDestination, Success: true}, {Kind: probe.KindDestination, Success: false}}) {
		t.Fatal("any external failure triggers")
	}
}

func TestCadenceSampler(t *testing.T) {
	s := CadenceSampler{Interval: 30 * time.Second}
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	if !s.Allow(t0, probe.ModeNormal) {
		t.Fatal("normal always allowed")
	}
	n := 0
	for i := 1; i <= 30; i++ { // 60s of 2s fast rounds
		if s.Allow(t0.Add(time.Duration(i)*2*time.Second), probe.ModeFast) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("expected 2 sampled fast rounds in 60s, got %d", n)
	}
}

type recProber struct {
	mu       sync.Mutex
	up       map[string]bool
	seen     []string
	timeouts []time.Duration
}

func (p *recProber) Probe(ctx context.Context, t probe.Target) probe.Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, t.Name)
	if dl, ok := ctx.Deadline(); ok {
		p.timeouts = append(p.timeouts, time.Until(dl))
	}
	return probe.Result{Target: t.Name, Kind: t.Kind, Success: p.up[t.Name], Latency: time.Millisecond}
}

func (p *recProber) reset() {
	p.mu.Lock()
	p.seen, p.timeouts = nil, nil
	p.mu.Unlock()
}

// End-to-end through the Runner: a 7s blip is pinned to 2s resolution, fast
// rounds skip AWS and use a 1s timeout, rows carry the mode, and the runner
// returns to the 30s cadence after the 60s tail.
func TestRunnerFastPollEndToEnd(t *testing.T) {
	cfg := config.Default()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp := &recProber{up: map[string]bool{"google-dns": true, "cloudflare-dns": true, "aws-us-east-1": true, "gateway": true}}
	r := New(cfg, fp, metrics.NewCollector(time.Hour), incident.NewEvaluator(cfg.SLA, nil), incident.NewStore(100), log.New(io.Discard, "", 0))
	r.SetDB(db)
	r.SetGateway("192.168.86.1", 0)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)

	if iv := r.Step(ctx, t0); iv != 30*time.Second {
		t.Fatalf("normal interval %s", iv)
	}
	// Internet drops just before the next normal round (t0+30s).
	setExt := func(v bool) {
		fp.mu.Lock()
		for _, n := range []string{"google-dns", "cloudflare-dns", "aws-us-east-1"} {
			fp.up[n] = v
		}
		fp.mu.Unlock()
	}
	setExt(false)
	now := t0.Add(30 * time.Second)
	iv := r.Step(ctx, now) // normal round fails -> fast
	if iv != 2*time.Second || r.Mode() != probe.ModeFast {
		t.Fatalf("expected fast after failure, got %s %s", iv, r.Mode())
	}
	fp.reset()
	for i := 0; i < 3; i++ { // down for 3 more fast rounds
		now = now.Add(iv)
		iv = r.Step(ctx, now)
	}
	fp.mu.Lock()
	for _, n := range fp.seen {
		if n == "aws-us-east-1" {
			t.Error("fast round probed aws")
		}
	}
	for _, d := range fp.timeouts {
		if d > time.Second {
			t.Errorf("fast probe timeout %s > 1s", d)
		}
	}
	fp.mu.Unlock()
	setExt(true)
	recAt := now.Add(iv)
	steps := 0
	for now = recAt; r.Mode() != probe.ModeNormal || steps == 0; steps++ {
		iv = r.Step(ctx, now)
		now = now.Add(iv)
		if steps > 100 {
			t.Fatal("never returned to normal")
		}
	}
	if iv != 30*time.Second || steps != 31 { // rounds at +0..+60s every 2s
		t.Fatalf("tail: steps=%d iv=%s", steps, iv)
	}
	outs, _ := db.Outages(t0, t0.Add(time.Hour))
	if len(outs) != 1 || outs[0].End == nil {
		t.Fatalf("outages %+v", outs)
	}
	if got := outs[0].End.Sub(outs[0].Start); got != 8*time.Second {
		t.Fatalf("outage should be pinned to the 2s rounds (8s), got %s", got)
	}
	rows, _ := db.ProbesSince(t0)
	modes := map[string]int{}
	for _, p := range rows {
		modes[p.Mode]++
	}
	if modes[probe.ModeNormal] != 8 || modes[probe.ModeFast] != (3+31)*3 {
		t.Fatalf("row modes %v", modes)
	}
}
