package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/outage"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/storage"
)

// Runner probes every target once per round (concurrently) so results line
// up in time, then feeds the round to metrics, the DB, the outage detector,
// and the incident evaluator.
type Runner struct {
	cfg       config.AppConfig
	prober    probe.Prober
	collector *metrics.Collector
	evaluator *incident.Evaluator
	store     *incident.Store
	db        *storage.DB // optional
	logger    *log.Logger
	gateway   *probe.Target

	mu       sync.Mutex
	detector outage.Detector
	outageID int64
	lastWall time.Time
	fast     *FastPoll
	sampler  CadenceSampler
}

func New(
	cfg config.AppConfig,
	prober probe.Prober,
	collector *metrics.Collector,
	evaluator *incident.Evaluator,
	store *incident.Store,
	logger *log.Logger,
) *Runner {
	normal := IntervalForProfile(cfg.Monitoring.Profile, cfg.Monitoring)
	return &Runner{
		cfg:       cfg,
		prober:    prober,
		collector: collector,
		evaluator: evaluator,
		store:     store,
		logger:    logger,
		fast:      NewFastPoll(normal, cfg.Monitoring.FastPoll),
		sampler:   CadenceSampler{Interval: normal},
	}
}

// Mode returns the polling mode of the next round (normal, fast, or slow).
func (r *Runner) Mode() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fast.Mode()
}

// NextInterval returns the delay until the next round for the current mode.
func (r *Runner) NextInterval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fast.Interval()
}

// RoundTargets returns the targets for the next round: everything in normal
// mode, or the gateway plus fast_poll.targets during a fast-poll window.
func (r *Runner) RoundTargets() []probe.Target {
	all := r.Targets()
	if r.Mode() == probe.ModeNormal {
		return all
	}
	return FastTargets(all, r.cfg.Monitoring.FastPoll.Targets)
}

// Step runs one round in the current mode and returns the delay until the next.
func (r *Runner) Step(ctx context.Context, started time.Time) time.Duration {
	r.RunRound(ctx, r.RoundTargets(), started)
	return r.NextInterval()
}

func (r *Runner) probeTimeout(mode string) time.Duration {
	if mode != probe.ModeNormal {
		if t := r.cfg.Monitoring.FastPoll.ProbeTimeout(); t > 0 {
			return t
		}
	}
	return r.cfg.Monitoring.ProbeTimeout()
}

// SetDB enables persistence.
func (r *Runner) SetDB(db *storage.DB) { r.db = db }

// SetGateway adds the local router as a probe target (kind gateway).
func (r *Runner) SetGateway(host string, port int) {
	r.gateway = &probe.Target{Name: "gateway", Host: host, Port: port, Kind: probe.KindGateway}
}

// Targets returns all targets probed each round.
func (r *Runner) Targets() []probe.Target {
	t := BuildTargets(r.cfg.Monitoring)
	if r.gateway != nil {
		t = append(t, *r.gateway)
	}
	return t
}

// Resume reconciles state left in the DB by a previous run: closes any
// outage left open at the last probe time and records a restart gap.
func (r *Runner) Resume(now time.Time) {
	if r.db == nil {
		return
	}
	last, ok, err := r.db.LastProbeTime()
	if err != nil {
		r.logger.Printf("resume: read last probe: %v", err)
		return
	}
	if !ok {
		return
	}
	if n, err := r.db.CloseDanglingOutages(last); err != nil {
		r.logger.Printf("resume: close dangling outages: %v", err)
	} else if n > 0 {
		r.logger.Printf("resume: closed %d outage(s) left open at %s", n, last.Format(time.RFC3339))
	}
	interval := r.cfg.Monitoring.ProbeInterval()
	// A gap is the time from the last record until the next expected round
	// would have been missed; anything under the threshold is normal cadence.
	if now.Sub(last) > r.cfg.Monitoring.GapThreshold() {
		g := storage.Gap{Start: last.Add(interval), End: now, Reason: "restart"}
		if err := r.db.InsertGap(g); err != nil {
			r.logger.Printf("resume: record gap: %v", err)
		} else {
			r.logger.Printf("gap recorded reason=restart start=%s end=%s", g.Start.Format(time.RFC3339), g.End.Format(time.RFC3339))
		}
	}
}

func (r *Runner) Start(ctx context.Context) {
	targets := r.Targets()
	if len(targets) == 0 {
		r.logger.Printf("no targets configured")
		return
	}
	interval := IntervalForProfile(r.cfg.Monitoring.Profile, r.cfg.Monitoring)
	r.logger.Printf("starting probe rounds: profile=%s targets=%d interval=%s", r.cfg.Monitoring.Profile, len(targets), interval)

	if r.fast.Enabled {
		fp := r.cfg.Monitoring.FastPoll
		r.logger.Printf("fast-poll on failure: interval=%s timeout=%s tail=%s cap=%s then %s targets=gateway+%v",
			r.fast.Fast, fp.ProbeTimeout(), r.fast.Tail, r.fast.MaxFast, r.fast.Slow, fp.Targets)
	}

	go func() {
		// Rounds are scheduled from each round's start, and the cadence is
		// re-read after every round so a fast-poll window takes effect at once.
		for {
			started := time.Now()
			next := r.Step(ctx, started)
			timer := time.NewTimer(time.Until(started.Add(next)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func BuildTargets(m config.MonitoringConfig) []probe.Target {
	targets := make([]probe.Target, 0, len(m.Destinations)+len(m.Peers))
	for _, d := range m.Destinations {
		targets = append(targets, probe.Target{Name: d.Name, Host: d.Host, Port: d.Port, Kind: probe.KindDestination})
	}
	if m.Profile == config.ProfileServerAssisted {
		for _, p := range m.Peers {
			targets = append(targets, probe.Target{Name: p.Name, Host: p.Host, Port: p.Port, Kind: probe.KindPeer})
		}
	}
	return targets
}

// TargetLatencyLimits maps target name to its latency_ms_max.
func TargetLatencyLimits(m config.MonitoringConfig) map[string]float64 {
	out := map[string]float64{}
	for _, t := range append(append([]config.TargetConfig{}, m.Destinations...), m.Peers...) {
		if t.LatencyMSMax > 0 {
			out[t.Name] = t.LatencyMSMax
		}
	}
	return out
}

func IntervalForProfile(profile config.Profile, m config.MonitoringConfig) time.Duration {
	if profile == config.ProfileServerAssisted {
		return time.Duration(m.ServerProbeIntervalMS) * time.Millisecond
	}
	return time.Duration(m.LocalProbeIntervalS) * time.Second
}

// RunRound probes all targets concurrently and processes the aligned results.
// started is the wall-clock time of the round (monotonic reading is stripped
// so time spent asleep is visible).
func (r *Runner) RunRound(ctx context.Context, targets []probe.Target, started time.Time) []probe.Result {
	roundTime := started.Round(0).UTC()
	mode := r.Mode()
	timeout := r.probeTimeout(mode)
	results := make([]probe.Result, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t probe.Target) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res := r.prober.Probe(pctx, t)
			res.Timestamp = roundTime // align the round
			res.Mode = mode
			results[i] = res
		}(i, t)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return results
	}
	r.processRound(roundTime, mode, results)
	return results
}

func (r *Runner) processRound(roundTime time.Time, mode string, results []probe.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sleep gap detection.
	if !r.lastWall.IsZero() && roundTime.Sub(r.lastWall) > r.cfg.Monitoring.GapThreshold() {
		g := storage.Gap{Start: r.lastWall.Add(r.cfg.Monitoring.ProbeInterval()), End: roundTime, Reason: "sleep"}
		r.logger.Printf("gap recorded reason=sleep start=%s end=%s", g.Start.Format(time.RFC3339), g.End.Format(time.RFC3339))
		if r.db != nil {
			if err := r.db.InsertGap(g); err != nil {
				r.logger.Printf("db: insert gap: %v", err)
			}
		}
		// An outage open across a sleep can't be trusted: close it at the last
		// observed round so the sleep isn't counted as downtime.
		if r.detector.Open() {
			r.closeOutage(r.lastWall)
			r.detector = outage.Detector{}
		}
		// Likewise a fast-poll window can't span a sleep.
		if r.fast.Mode() != probe.ModeNormal {
			r.logger.Printf("FASTPOLL reset after gap")
			r.fast.Reset()
		}
	}
	r.lastWall = roundTime

	// Round-counting consumers see at most one round per normal interval.
	sampled := r.sampler.Allow(roundTime, mode)
	if sampled {
		for _, res := range results {
			r.collector.Add(res)
		}
	}
	if r.db != nil {
		if err := r.db.InsertProbes(results); err != nil {
			r.logger.Printf("db: insert probes: %v", err)
		}
	}

	tr := r.detector.Observe(roundTime, results)
	switch {
	case tr.Opened:
		r.logger.Printf("OUTAGE START cause=%s at=%s", tr.Cause, tr.Start.Format(time.RFC3339))
		r.recordIncident(incident.Event{Time: tr.Start, Target: "internet", Severity: "critical", State: "open",
			Message: "Internet down: all external destinations failed (cause=" + tr.Cause + ")"})
		if r.db != nil {
			id, err := r.db.OpenOutage(tr.Start, tr.Cause)
			if err != nil {
				r.logger.Printf("db: open outage: %v", err)
			}
			r.outageID = id
		}
	case tr.Closed:
		r.logger.Printf("OUTAGE END cause=%s start=%s end=%s duration=%s", tr.Cause, tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339), tr.End.Sub(tr.Start))
		r.closeOutage(tr.End)
	}

	if sampled {
		for _, res := range results {
			summary := r.collector.Summary(res.Target, 5*time.Minute, roundTime.Add(time.Millisecond))
			if ev := r.evaluator.Observe(res, summary, roundTime); ev != nil {
				r.recordIncident(*ev)
			}
		}
	}

	if tr, ok := r.fast.Observe(roundTime, RoundFailed(results)); ok {
		switch tr.To {
		case probe.ModeFast:
			r.logger.Printf("FASTPOLL start at=%s interval=%s (external probe failed)", tr.At.Format(time.RFC3339), r.fast.Fast)
		case probe.ModeSlow:
			r.logger.Printf("FASTPOLL cap reached after %s, slowing to %s at=%s", tr.Window.Round(time.Second), r.fast.Slow, tr.At.Format(time.RFC3339))
		case probe.ModeNormal:
			r.logger.Printf("FASTPOLL end at=%s window=%s, back to %s", tr.At.Format(time.RFC3339), tr.Window.Round(time.Second), r.fast.Normal)
		}
	}
}

func (r *Runner) closeOutage(end time.Time) {
	r.recordIncident(incident.Event{Time: end, Target: "internet", Severity: "info", State: "resolved", Message: "Internet connectivity restored"})
	if r.db != nil && r.outageID != 0 {
		if err := r.db.CloseOutage(r.outageID, end); err != nil {
			r.logger.Printf("db: close outage: %v", err)
		}
	}
	r.outageID = 0
}

func (r *Runner) recordIncident(ev incident.Event) {
	r.store.Add(ev)
	r.logger.Printf("incident target=%s state=%s severity=%s msg=%s", ev.Target, ev.State, ev.Severity, ev.Message)
	if r.db != nil {
		if err := r.db.InsertIncident(ev); err != nil {
			r.logger.Printf("db: insert incident: %v", err)
		}
	}
}
