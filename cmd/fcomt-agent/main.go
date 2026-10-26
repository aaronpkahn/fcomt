package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaron/fcomt-agent/internal/agent"
	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/report"
	"github.com/aaron/fcomt-agent/internal/storage"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to YAML config file")
		listenAddr = flag.String("listen", "", "override API listen address")
		dbPath     = flag.String("db", "", "override SQLite database path")
		reportKind = flag.String("report", "", "print a report and exit: daily|monthly")
		reportDate = flag.String("date", "", "report period: YYYY-MM-DD (daily) or YYYY-MM (monthly); default today/this month (America/Los_Angeles)")
		format     = flag.String("format", "text", "report format: text|json|csv")
	)
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *listenAddr != "" {
		cfg.API.Listen = *listenAddr
	}
	if *dbPath != "" {
		cfg.Storage.Path = *dbPath
	}

	if *reportKind != "" {
		if err := printReport(cfg, *reportKind, *reportDate, *format); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("create app: %v", err)
	}

	log.Printf("starting FComT agent: profile=%s api=%s interval=%s", cfg.Monitoring.Profile, cfg.API.Listen, cfg.Monitoring.ProbeInterval())
	if err := app.Run(ctx); err != nil {
		log.Fatalf("run app: %v", err)
	}
}

func printReport(cfg config.AppConfig, kind, date, format string) error {
	path := agent.DBPath(cfg)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("database %s: %w", path, err)
	}
	db, err := storage.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now()
	var r report.Report
	switch kind {
	case "daily":
		if date == "" {
			date = now.In(report.Location).Format("2006-01-02")
		}
		r, err = report.Daily(db, date, now)
	case "monthly":
		if date == "" {
			date = now.In(report.Location).Format("2006-01")
		}
		r, err = report.Monthly(db, date, now)
	default:
		return fmt.Errorf("unknown report %q (use daily or monthly)", kind)
	}
	if err != nil {
		return err
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "csv":
		return report.WriteCSV(os.Stdout, r)
	default:
		printText(r)
		return nil
	}
}

func printText(r report.Report) {
	d := func(s float64) string { return (time.Duration(s) * time.Second).String() }
	ts := func(t time.Time) string { return t.In(report.Location).Format("2006-01-02 15:04:05 MST") }
	fmt.Printf("FComT %s report for %s (%s)\n", r.Kind, r.Period, r.Timezone)
	fmt.Printf("  Uptime:        %.3f%% of monitored time (%s monitored)\n", r.UptimePct, d(r.MonitoredSeconds))
	fmt.Printf("  Outages:       %d, total downtime %s (ISP %s, local %s)\n", r.OutageCount, d(r.DowntimeSeconds), d(r.ISPDowntimeSeconds), d(r.LocalDowntimeSeconds))
	fmt.Printf("  Gaps:          %d, %s not monitored (sleep/restart)\n", r.GapCount, d(r.GapSeconds))
	for _, o := range r.Outages {
		end := "ongoing"
		if o.End != nil {
			end = ts(*o.End)
		}
		fmt.Printf("  OUTAGE %s -> %s  %s  cause=%s\n", ts(o.Start), end, d(o.DurationSeconds), o.Cause)
	}
	for _, g := range r.Gaps {
		fmt.Printf("  GAP    %s -> %s  %s  reason=%s\n", ts(g.Start), ts(g.End), d(g.DurationSeconds), g.Reason)
	}
}

func loadConfig(path string) (config.AppConfig, error) {
	if path == "" {
		cfg := config.Default()
		return cfg, cfg.Validate()
	}
	return config.Load(path)
}
