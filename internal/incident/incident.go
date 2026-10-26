package incident

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
)

type Event struct {
	Time     time.Time `json:"time"`
	Target   string    `json:"target"`
	Severity string    `json:"severity"`
	State    string    `json:"state"`
	Message  string    `json:"message"`
}

// Evaluator opens a per-target incident only after the target has been bad
// for SLA.ConsecutiveRounds consecutive probe rounds, so a single dropped
// probe never raises a critical incident. "Bad" means the round's probe
// failed, or the rolling average latency/jitter exceeds an enabled limit.
type Evaluator struct {
	mu        sync.Mutex
	sla       config.SLAConfig
	latency   map[string]float64
	active    map[string]bool
	badStreak map[string]int
}

// NewEvaluator builds an evaluator. targetLatencyMS maps target name to its
// own latency limit in ms (0 or missing = use the global SLA limit, which is
// off by default).
func NewEvaluator(sla config.SLAConfig, targetLatencyMS map[string]float64) *Evaluator {
	if targetLatencyMS == nil {
		targetLatencyMS = map[string]float64{}
	}
	return &Evaluator{
		sla:       sla,
		latency:   targetLatencyMS,
		active:    make(map[string]bool),
		badStreak: make(map[string]int),
	}
}

func (e *Evaluator) required() int {
	if e.sla.ConsecutiveRounds < 1 {
		return 1
	}
	return e.sla.ConsecutiveRounds
}

// Observe evaluates one probe round for a target. summary is the rolling
// window used for latency/jitter checks.
func (e *Evaluator) Observe(res probe.Result, summary metrics.Summary, now time.Time) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := res.Target
	breaches := e.breaches(res, summary)
	wasDegraded := e.active[target]

	if len(breaches) > 0 {
		e.badStreak[target]++
	} else {
		e.badStreak[target] = 0
	}

	switch {
	case len(breaches) > 0 && !wasDegraded && e.badStreak[target] >= e.required():
		e.active[target] = true
		return &Event{
			Time:     now.UTC(),
			Target:   target,
			Severity: "critical",
			State:    "open",
			Message:  fmt.Sprintf("SLA breach for %d consecutive rounds: %s", e.badStreak[target], strings.Join(breaches, ", ")),
		}
	case len(breaches) == 0 && wasDegraded:
		e.active[target] = false
		return &Event{
			Time:     now.UTC(),
			Target:   target,
			Severity: "info",
			State:    "resolved",
			Message:  "Target recovered within SLA thresholds",
		}
	default:
		return nil
	}
}

func (e *Evaluator) breaches(res probe.Result, summary metrics.Summary) []string {
	breaches := make([]string, 0, 3)
	if !res.Success {
		msg := "probe failed"
		if res.Error != "" {
			msg += " (" + res.Error + ")"
		}
		breaches = append(breaches, msg)
	}
	limit := e.latency[res.Target]
	if limit == 0 {
		limit = e.sla.LatencyMSMax
	}
	if limit > 0 && summary.SuccessCount > 0 && summary.AvgLatencyMS > limit {
		breaches = append(breaches, fmt.Sprintf("avg latency %.2fms > %.2fms", summary.AvgLatencyMS, limit))
	}
	if e.sla.JitterMSMax > 0 && summary.JitterMS > e.sla.JitterMSMax {
		breaches = append(breaches, fmt.Sprintf("jitter %.2fms > %.2fms", summary.JitterMS, e.sla.JitterMSMax))
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

// Between returns events with from <= Time <= to.
func (s *Store) Between(from, to time.Time) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, 0)
	for _, e := range s.events {
		if !e.Time.Before(from) && !e.Time.After(to) {
			out = append(out, e)
		}
	}
	return out
}
