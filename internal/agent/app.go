package agent

import (
	"context"
	"log"
	"time"

	"github.com/aaron/fcomt-agent/internal/api"
	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/scheduler"
)

type App struct {
	cfg    config.AppConfig
	runner *scheduler.Runner
	api    *api.Server
}

func New(cfg config.AppConfig) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	collector := metrics.NewCollector(7 * 24 * time.Hour)
	store := incident.NewStore(1000)
	evaluator := incident.NewEvaluator(cfg.SLA)
	prober := probe.TCPProber{Timeout: cfg.Monitoring.ProbeTimeout()}

	logger := log.Default()
	runner := scheduler.New(cfg, prober, collector, evaluator, store, logger)
	srv := api.New(cfg, collector, store)

	return &App{
		cfg:    cfg,
		runner: runner,
		api:    srv,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	a.runner.Start(ctx)
	return a.api.Run(ctx)
}
