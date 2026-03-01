package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
)

type Runner struct {
	cfg       config.AppConfig
	prober    probe.Prober
	collector *metrics.Collector
	evaluator *incident.Evaluator
	store     *incident.Store
	logger    *log.Logger

	lastEvalMu sync.Mutex
	lastEval   map[string]time.Time
	evalEvery  time.Duration
}

func New(
	cfg config.AppConfig,
	prober probe.Prober,
	collector *metrics.Collector,
	evaluator *incident.Evaluator,
	store *incident.Store,
	logger *log.Logger,
) *Runner {
	return &Runner{
		cfg:       cfg,
		prober:    prober,
		collector: collector,
		evaluator: evaluator,
		store:     store,
		logger:    logger,
		lastEval:  make(map[string]time.Time),
		evalEvery: EvaluationIntervalForProfile(cfg.Monitoring.Profile),
	}
}

func (r *Runner) Start(ctx context.Context) {
	targets := BuildTargets(r.cfg.Monitoring)
	if len(targets) == 0 {
		r.logger.Printf("no targets configured")
		return
	}

	interval := IntervalForProfile(r.cfg.Monitoring.Profile, r.cfg.Monitoring)
	r.logger.Printf("starting probe loops: profile=%s targets=%d interval=%s", r.cfg.Monitoring.Profile, len(targets), interval)

	var wg sync.WaitGroup
	for _, t := range targets {
		target := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runTargetLoop(ctx, target, interval)
		}()
	}

	go func() {
		<-ctx.Done()
		wg.Wait()
	}()
}

func BuildTargets(m config.MonitoringConfig) []probe.Target {
	targets := make([]probe.Target, 0, len(m.Destinations)+len(m.Peers))
	for _, d := range m.Destinations {
		targets = append(targets, probe.Target{
			Name: d.Name,
			Host: d.Host,
			Port: d.Port,
			Kind: probe.KindDestination,
		})
	}
	if m.Profile == config.ProfileServerAssisted {
		for _, p := range m.Peers {
			targets = append(targets, probe.Target{
				Name: p.Name,
				Host: p.Host,
				Port: p.Port,
				Kind: probe.KindPeer,
			})
		}
	}
	return targets
}

func IntervalForProfile(profile config.Profile, m config.MonitoringConfig) time.Duration {
	if profile == config.ProfileServerAssisted {
		return time.Duration(m.ServerProbeIntervalMS) * time.Millisecond
	}
	return time.Duration(m.LocalProbeIntervalS) * time.Second
}

func EvaluationIntervalForProfile(profile config.Profile) time.Duration {
	if profile == config.ProfileServerAssisted {
		return 10 * time.Second
	}
	return time.Minute
}

func (r *Runner) runTargetLoop(ctx context.Context, target probe.Target, interval time.Duration) {
	r.runProbe(ctx, target)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runProbe(ctx, target)
		}
	}
}

func (r *Runner) runProbe(ctx context.Context, target probe.Target) {
	probeCtx, cancel := context.WithTimeout(ctx, r.cfg.Monitoring.ProbeTimeout())
	defer cancel()

	res := r.prober.Probe(probeCtx, target)
	r.collector.Add(res)

	now := time.Now().UTC()
	if !r.shouldEvaluate(target.Name, now) {
		return
	}

	summary := r.collector.Summary(target.Name, 5*time.Minute, now)
	if event := r.evaluator.Evaluate(summary, now); event != nil {
		r.store.Add(*event)
		r.logger.Printf("incident target=%s state=%s severity=%s msg=%s", event.Target, event.State, event.Severity, event.Message)
	}
}

func (r *Runner) shouldEvaluate(target string, now time.Time) bool {
	r.lastEvalMu.Lock()
	defer r.lastEvalMu.Unlock()

	last, ok := r.lastEval[target]
	if !ok || now.Sub(last) >= r.evalEvery {
		r.lastEval[target] = now
		return true
	}
	return false
}
