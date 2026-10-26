package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aaron/fcomt-agent/internal/api"
	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/scheduler"
	"github.com/aaron/fcomt-agent/internal/storage"
)

type App struct {
	cfg    config.AppConfig
	runner *scheduler.Runner
	api    *api.Server
	db     *storage.DB
}

// DBPath resolves the configured database path.
func DBPath(cfg config.AppConfig) string {
	if cfg.Storage.Path != "" {
		return cfg.Storage.Path
	}
	return storage.DefaultPath()
}

func New(cfg config.AppConfig) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	logger := log.Default()

	db, err := storage.Open(DBPath(cfg))
	if err != nil {
		return nil, err
	}
	logger.Printf("database: %s", DBPath(cfg))

	collector := metrics.NewCollector(7 * 24 * time.Hour)
	store := incident.NewStore(1000)
	if err := loadHistory(cfg, db, collector, store); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load history: %w", err)
	}

	evaluator := incident.NewEvaluator(cfg.SLA, scheduler.TargetLatencyLimits(cfg.Monitoring))
	timeout := cfg.Monitoring.ProbeTimeout()
	prober := probe.MultiProber{
		Default: probe.TCPProber{Timeout: timeout},
		Gateway: probe.GatewayProber{Timeout: timeout},
	}

	runner := scheduler.New(cfg, prober, collector, evaluator, store, logger)
	runner.SetDB(db)
	if gw := cfg.Monitoring.Gateway; gw.Enabled {
		host := gw.Host
		if host == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			host, err = probe.DetectGateway(ctx)
			cancel()
			if err != nil {
				logger.Printf("gateway probe disabled: %v", err)
			}
		}
		if host != "" {
			runner.SetGateway(host, gw.Port)
			logger.Printf("gateway target: %s", host)
		}
	}

	srv := api.New(cfg, collector, store)
	srv.SetDB(db)

	return &App{cfg: cfg, runner: runner, api: srv, db: db}, nil
}

func loadHistory(cfg config.AppConfig, db *storage.DB, c *metrics.Collector, s *incident.Store) error {
	hours := cfg.Storage.LoadHistoryHours
	if hours <= 0 {
		hours = 7 * 24
	}
	now := time.Now().UTC()
	since := now.Add(-time.Duration(hours) * time.Hour)
	probes, err := db.ProbesSince(since)
	if err != nil {
		return err
	}
	for _, p := range probes {
		c.Add(p)
	}
	events, err := db.Incidents(since, now, 1000)
	if err != nil {
		return err
	}
	for _, e := range events {
		s.Add(e)
	}
	log.Printf("loaded history: %d probes, %d incidents since %s", len(probes), len(events), since.Format(time.RFC3339))
	return nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.db.Close()
	a.runner.Resume(time.Now())
	a.runner.Start(ctx)
	return a.api.Run(ctx)
}
