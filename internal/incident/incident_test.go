package incident

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/metrics"
)

func TestEvaluatorOpenAndResolve(t *testing.T) {
	e := NewEvaluator(config.SLAConfig{
		LatencyMSMax:       40,
		JitterMSMax:        10,
		PacketLossPctMax:   1,
		AvailabilityPctMin: 99,
	})

	now := time.Now().UTC()
	bad := metrics.Summary{
		Target:          "google",
		Count:           10,
		AvgLatencyMS:    120,
		JitterMS:        20,
		LossPct:         10,
		AvailabilityPct: 90,
	}
	ev := e.Evaluate(bad, now)
	if ev == nil || ev.State != "open" {
		t.Fatalf("expected open incident, got %+v", ev)
	}

	good := metrics.Summary{
		Target:          "google",
		Count:           10,
		AvgLatencyMS:    20,
		JitterMS:        3,
		LossPct:         0,
		AvailabilityPct: 100,
	}
	ev = e.Evaluate(good, now.Add(1*time.Minute))
	if ev == nil || ev.State != "resolved" {
		t.Fatalf("expected resolved incident, got %+v", ev)
	}
}
