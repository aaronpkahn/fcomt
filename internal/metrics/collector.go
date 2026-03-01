package metrics

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

type Summary struct {
	Target          string    `json:"target"`
	Window          string    `json:"window"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	Count           int       `json:"count"`
	SuccessCount    int       `json:"success_count"`
	FailureCount    int       `json:"failure_count"`
	LossPct         float64   `json:"loss_pct"`
	AvailabilityPct float64   `json:"availability_pct"`
	AvgLatencyMS    float64   `json:"avg_latency_ms"`
	P95LatencyMS    float64   `json:"p95_latency_ms"`
	JitterMS        float64   `json:"jitter_ms"`
}

type TimePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
	LatencyMS float64   `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
}

type TargetSeries struct {
	Target string      `json:"target"`
	Points []TimePoint `json:"points"`
}

type Collector struct {
	mu      sync.RWMutex
	maxAge  time.Duration
	byKey   map[string][]probe.Result
	latest  map[string]probe.Result
	lastAdd time.Time
}

func NewCollector(maxAge time.Duration) *Collector {
	return &Collector{
		maxAge: maxAge,
		byKey:  make(map[string][]probe.Result),
		latest: make(map[string]probe.Result),
	}
}

func (c *Collector) Add(res probe.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastAdd = res.Timestamp
	series := append(c.byKey[res.Target], res)
	c.byKey[res.Target] = prune(series, res.Timestamp.Add(-c.maxAge))
	c.latest[res.Target] = res
}

func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byKey = make(map[string][]probe.Result)
	c.latest = make(map[string]probe.Result)
	c.lastAdd = time.Time{}
}

func (c *Collector) Latest(target string) (probe.Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if target != "" {
		r, ok := c.latest[target]
		return r, ok
	}

	var newest probe.Result
	var ok bool
	for _, r := range c.latest {
		if !ok || r.Timestamp.After(newest.Timestamp) {
			newest = r
			ok = true
		}
	}
	return newest, ok
}

func (c *Collector) LatestAll() []probe.Result {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]probe.Result, 0, len(c.latest))
	for _, res := range c.latest {
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Target < out[j].Target
	})
	return out
}

func (c *Collector) Summary(target string, window time.Duration, now time.Time) Summary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	start := now.Add(-window)
	summary := Summary{
		Target: target,
		Window: window.String(),
		Start:  start,
		End:    now,
	}

	series := c.byKey[target]
	samples := filterSamples(series, start, now)
	calc(&summary, samples)
	return summary
}

func (c *Collector) Summaries(window time.Duration, now time.Time) []Summary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	start := now.Add(-window)
	out := make([]Summary, 0, len(c.byKey))
	for target, series := range c.byKey {
		summary := Summary{
			Target: target,
			Window: window.String(),
			Start:  start,
			End:    now,
		}
		calc(&summary, filterSamples(series, start, now))
		out = append(out, summary)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Target < out[j].Target
	})
	return out
}

func (c *Collector) TimeSeries(target string, window time.Duration, now time.Time) []TargetSeries {
	c.mu.RLock()
	defer c.mu.RUnlock()

	start := now.Add(-window)
	out := make([]TargetSeries, 0)

	appendSeries := func(target string, series []probe.Result) {
		samples := filterSamples(series, start, now)
		points := make([]TimePoint, 0, len(samples))
		for _, sample := range samples {
			latencyMS := 0.0
			if sample.Success {
				latencyMS = float64(sample.Latency.Microseconds()) / 1000.0
			}
			points = append(points, TimePoint{
				Timestamp: sample.Timestamp,
				Success:   sample.Success,
				LatencyMS: latencyMS,
				Error:     sample.Error,
			})
		}
		sort.Slice(points, func(i, j int) bool {
			return points[i].Timestamp.Before(points[j].Timestamp)
		})
		out = append(out, TargetSeries{
			Target: target,
			Points: points,
		})
	}

	if target != "" {
		appendSeries(target, c.byKey[target])
		return out
	}

	targets := make([]string, 0, len(c.byKey))
	for key := range c.byKey {
		targets = append(targets, key)
	}
	sort.Strings(targets)
	for _, key := range targets {
		appendSeries(key, c.byKey[key])
	}
	return out
}

func prune(series []probe.Result, cutoff time.Time) []probe.Result {
	idx := 0
	for idx < len(series) && series[idx].Timestamp.Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return series
	}
	out := make([]probe.Result, len(series)-idx)
	copy(out, series[idx:])
	return out
}

func filterSamples(series []probe.Result, start, end time.Time) []probe.Result {
	out := make([]probe.Result, 0, len(series))
	for _, s := range series {
		if (s.Timestamp.Equal(start) || s.Timestamp.After(start)) && (s.Timestamp.Equal(end) || s.Timestamp.Before(end)) {
			out = append(out, s)
		}
	}
	return out
}

func calc(summary *Summary, samples []probe.Result) {
	summary.Count = len(samples)
	if len(samples) == 0 {
		return
	}

	latencies := make([]float64, 0, len(samples))
	successive := make([]float64, 0, len(samples))

	var lastLatency float64
	var hasLast bool
	var latencySum float64

	for _, s := range samples {
		if s.Success {
			summary.SuccessCount++
			ms := float64(s.Latency.Microseconds()) / 1000.0
			latencies = append(latencies, ms)
			latencySum += ms
			if hasLast {
				successive = append(successive, math.Abs(ms-lastLatency))
			}
			lastLatency = ms
			hasLast = true
		} else {
			summary.FailureCount++
		}
	}

	if summary.Count > 0 {
		summary.LossPct = (float64(summary.FailureCount) / float64(summary.Count)) * 100
		summary.AvailabilityPct = (float64(summary.SuccessCount) / float64(summary.Count)) * 100
	}

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		summary.AvgLatencyMS = latencySum / float64(len(latencies))
		summary.P95LatencyMS = percentile(latencies, 0.95)
	}
	if len(successive) > 0 {
		var sum float64
		for _, v := range successive {
			sum += v
		}
		summary.JitterMS = sum / float64(len(successive))
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(float64(len(sorted))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
