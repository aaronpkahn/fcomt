package metrics

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

func TestSummaryCalculations(t *testing.T) {
	c := NewCollector(24 * time.Hour)
	now := time.Now().UTC()

	c.Add(probe.Result{Target: "google", Timestamp: now.Add(-4 * time.Minute), Success: true, Latency: 10 * time.Millisecond})
	c.Add(probe.Result{Target: "google", Timestamp: now.Add(-3 * time.Minute), Success: true, Latency: 30 * time.Millisecond})
	c.Add(probe.Result{Target: "google", Timestamp: now.Add(-2 * time.Minute), Success: false})
	c.Add(probe.Result{Target: "google", Timestamp: now.Add(-1 * time.Minute), Success: true, Latency: 20 * time.Millisecond})

	s := c.Summary("google", 5*time.Minute, now)
	if s.Count != 4 {
		t.Fatalf("expected 4 samples, got %d", s.Count)
	}
	if s.SuccessCount != 3 || s.FailureCount != 1 {
		t.Fatalf("expected success=3 failure=1, got success=%d failure=%d", s.SuccessCount, s.FailureCount)
	}
	if s.LossPct < 24.9 || s.LossPct > 25.1 {
		t.Fatalf("expected ~25%% loss, got %.2f", s.LossPct)
	}
	if s.AvgLatencyMS < 19.9 || s.AvgLatencyMS > 20.1 {
		t.Fatalf("expected avg ~20ms, got %.2f", s.AvgLatencyMS)
	}
	if s.JitterMS <= 0 {
		t.Fatalf("expected jitter > 0, got %.2f", s.JitterMS)
	}
}
