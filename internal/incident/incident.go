package incident

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/metrics"
)

type Event struct {
	Time     time.Time `json:"time"`
	Target   string    `json:"target"`
	Severity string    `json:"severity"`
	State    string    `json:"state"`
	Message  string    `json:"message"`
}

type Evaluator struct {
	mu     sync.Mutex
	sla    config.SLAConfig
	active map[string]bool
}

func NewEvaluator(sla config.SLAConfig) *Evaluator {
	return &Evaluator{
		sla:    sla,
		active: make(map[string]bool),
	}
}

func (e *Evaluator) Evaluate(summary metrics.Summary, now time.Time) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	if summary.Count == 0 {
		return nil
	}

	breaches := e.breaches(summary)
	isDegraded := len(breaches) > 0
	wasDegraded := e.active[summary.Target]

	switch {
	case isDegraded && !wasDegraded:
		e.active[summary.Target] = true
		return &Event{
			Time:     now.UTC(),
			Target:   summary.Target,
			Severity: "critical",
			State:    "open",
			Message:  "SLA breach detected: " + strings.Join(breaches, ", "),
		}
	case !isDegraded && wasDegraded:
		e.active[summary.Target] = false
		return &Event{
			Time:     now.UTC(),
			Target:   summary.Target,
			Severity: "info",
			State:    "resolved",
			Message:  "Target recovered within SLA thresholds",
		}
	default:
		return nil
	}
}

func (e *Evaluator) breaches(summary metrics.Summary) []string {
	breaches := make([]string, 0, 4)
	if e.sla.LatencyMSMax > 0 && summary.AvgLatencyMS > e.sla.LatencyMSMax {
		breaches = append(breaches, fmt.Sprintf("avg latency %.2fms > %.2fms", summary.AvgLatencyMS, e.sla.LatencyMSMax))
	}
	if e.sla.JitterMSMax > 0 && summary.JitterMS > e.sla.JitterMSMax {
		breaches = append(breaches, fmt.Sprintf("jitter %.2fms > %.2fms", summary.JitterMS, e.sla.JitterMSMax))
	}
	if e.sla.PacketLossPctMax > 0 && summary.LossPct > e.sla.PacketLossPctMax {
		breaches = append(breaches, fmt.Sprintf("loss %.2f%% > %.2f%%", summary.LossPct, e.sla.PacketLossPctMax))
	}
	if e.sla.AvailabilityPctMin > 0 && summary.AvailabilityPct < e.sla.AvailabilityPctMin {
		breaches = append(breaches, fmt.Sprintf("availability %.2f%% < %.2f%%", summary.AvailabilityPct, e.sla.AvailabilityPctMin))
	}
	return breaches
}

type Store struct {
	mu     sync.RWMutex
	limit  int
	events []Event
}

func NewStore(limit int) *Store {
	return &Store{
		limit:  limit,
		events: make([]Event, 0, limit),
	}
}

func (s *Store) Add(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, e)
	if len(s.events) > s.limit {
		s.events = append([]Event(nil), s.events[len(s.events)-s.limit:]...)
	}
}

func (s *Store) List() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:0]
}
