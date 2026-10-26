package incident

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
)

func TestSingleFailureDoesNotOpenIncident(t *testing.T) {
	e := NewEvaluator(config.SLAConfig{ConsecutiveRounds: 2}, nil)
	now := time.Now().UTC()
	fail := probe.Result{Target: "google", Success: false}
	ok := probe.Result{Target: "google", Success: true}
	if ev := e.Observe(fail, metrics.Summary{}, now); ev != nil {
		t.Fatalf("one failure should not open: %+v", ev)
	}
	if ev := e.Observe(ok, metrics.Summary{}, now); ev != nil {
		t.Fatalf("unexpected event %+v", ev)
	}
	if ev := e.Observe(fail, metrics.Summary{}, now); ev != nil {
		t.Fatalf("streak should have reset: %+v", ev)
	}
	ev := e.Observe(fail, metrics.Summary{}, now)
	if ev == nil || ev.State != "open" {
		t.Fatalf("expected open after 2 consecutive failures, got %+v", ev)
	}
	ev = e.Observe(ok, metrics.Summary{}, now.Add(time.Minute))
	if ev == nil || ev.State != "resolved" {
		t.Fatalf("expected resolved, got %+v", ev)
	}
}

func TestPerTargetLatency(t *testing.T) {
	e := NewEvaluator(config.SLAConfig{ConsecutiveRounds: 1}, map[string]float64{"google": 40})
	now := time.Now().UTC()
	slow := metrics.Summary{SuccessCount: 5, AvgLatencyMS: 90}
	if ev := e.Observe(probe.Result{Target: "aws", Success: true}, slow, now); ev != nil {
		t.Fatalf("aws has no limit, got %+v", ev)
	}
	if ev := e.Observe(probe.Result{Target: "google", Success: true}, slow, now); ev == nil || ev.State != "open" {
		t.Fatalf("google over 40ms should open, got %+v", ev)
	}
}

func TestStoreBetween(t *testing.T) {
	s := NewStore(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		s.Add(Event{Time: base.Add(time.Duration(i) * time.Hour)})
	}
	if got := len(s.Between(base.Add(time.Hour), base.Add(3*time.Hour))); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}
