package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/examples"
	"github.com/aaron/fcomt-agent/internal/export"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/report"
	"github.com/aaron/fcomt-agent/internal/storage"
)

//go:embed ui/*
var uiFS embed.FS

type Server struct {
	cfg       config.AppConfig
	collector *metrics.Collector
	store     *incident.Store
	examples  *examples.Library
	db        *storage.DB
}

// SetDB enables DB-backed endpoints (reports, outages, windowed evidence).
func (s *Server) SetDB(db *storage.DB) { s.db = db }

func New(cfg config.AppConfig, collector *metrics.Collector, store *incident.Store) *Server {
	return &Server{
		cfg:       cfg,
		collector: collector,
		store:     store,
		examples:  examples.NewLibrary(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fsSub(uiFS, "ui")
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/v1/config", s.handleConfig)
	mux.HandleFunc("/v1/metrics/latest", s.handleLatest)
	mux.HandleFunc("/v1/metrics/summary", s.handleSummary)
	mux.HandleFunc("/v1/metrics/timeseries", s.handleTimeSeries)
	mux.HandleFunc("/v1/incidents", s.handleIncidents)
	mux.HandleFunc("/v1/export/evidence", s.handleEvidence)
	mux.HandleFunc("/v1/outages", s.handleOutages)
	mux.HandleFunc("/v1/dashboard", s.handleDashboard)
	mux.HandleFunc("/chart.png", s.handleChartPNG)
	mux.HandleFunc("/v1/reports/daily", s.handleReport("daily"))
	mux.HandleFunc("/v1/reports/monthly", s.handleReport("monthly"))
	mux.HandleFunc("/v1/examples", s.handleExamples)
	mux.HandleFunc("/v1/examples/seed", s.handleSeedExample)
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.cfg.API.Listen,
		Handler: s.Handler(),
	}

	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(done)
	}()

	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target != "" {
		if latest, ok := s.collector.Latest(target); ok {
			writeJSON(w, http.StatusOK, latest)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.collector.LatestAll())
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	target := r.URL.Query().Get("target")
	if target != "" {
		writeJSON(w, http.StatusOK, s.collector.Summary(target, window, now))
		return
	}
	writeJSON(w, http.StatusOK, s.collector.Summaries(window, now))
}

func (s *Server) handleTimeSeries(w http.ResponseWriter, r *http.Request) {
	window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	target := r.URL.Query().Get("target")
	writeJSON(w, http.StatusOK, map[string]any{
		"window": window.String(),
		"start":  now.Add(-window).UTC(),
		"end":    now.UTC(),
		"series": s.collector.TimeSeries(target, window, now),
	})
}

func (s *Server) handleIncidents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.List())
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	bundle, err := export.BuildEvidence(s.cfg, s.collector, s.store, s.db, window, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleExamples(w http.ResponseWriter, _ *http.Request) {
	if err := s.examples.LoadError(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.examples.List())
}

func (s *Server) handleSeedExample(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.API.EnableDemo {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo seeding disabled; set api.enable_demo: true"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing name query parameter"})
		return
	}
	meta, err := s.examples.Seed(name, s.collector, s.store, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seeded":    meta,
		"message":   "example scenario loaded",
		"summaries": s.collector.Summaries(24*time.Hour, time.Now().UTC()),
	})
}

func (s *Server) handleOutages(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}
	window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	outs, err := s.db.Outages(now.Add(-window), now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	gaps, err := s.db.Gaps(now.Add(-window), now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if outs == nil {
		outs = []storage.Outage{}
	}
	if gaps == nil {
		gaps = []storage.Gap{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"outages": outs, "gaps": gaps})
}

type dashTarget struct {
	Target       string  `json:"target"`
	Kind         string  `json:"kind"`
	Count        int     `json:"count"`
	SuccessCount int     `json:"success_count"`
	UptimePct    float64 `json:"uptime_pct"`
	// FastCount rows came from fast-poll windows. Uptime is time-weighted
	// (MonitoredSeconds/UpSeconds), so those short-interval rows don't skew it.
	FastCount        int     `json:"fast_count"`
	MonitoredSeconds float64 `json:"monitored_seconds"`
	UpSeconds        float64 `json:"up_seconds"`
}

// handleDashboard serves chart data read from SQLite: aligned per-round
// latency series (null = failed probe), per-target uptime, and the
// incident/outage/gap lists for the window.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}
	window, err := parseWindow(r.URL.Query().Get("window"))
	if r.URL.Query().Get("window") == "" {
		window = time.Hour
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	d, err := s.dashboardData(window)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// dashData is the SQLite-backed payload shared by /v1/dashboard and /chart.png.
type dashData struct {
	Window    string           `json:"window"`
	Source    string           `json:"source"`
	RawRounds int              `json:"raw_rounds"`
	Points    int              `json:"points"`
	Start     time.Time        `json:"start"`
	End       time.Time        `json:"end"`
	TimesMS   []int64          `json:"times_ms"`
	Targets   []*dashTarget    `json:"targets"`
	Latency   [][]*float64     `json:"latency"`
	Incidents []incident.Event `json:"incidents"`
	Outages   []storage.Outage `json:"outages"`
	Gaps      []storage.Gap    `json:"gaps"`
	// FastRounds counts fast/slow-mode rounds in the window; they are left out
	// of the latency series (which stays at the normal cadence) and shown as
	// FastWindows instead. Outage start/end already use the fine-grained rows.
	FastRounds  int                 `json:"fast_rounds"`
	FastWindows []report.FastWindow `json:"fast_windows"`
}

func (s *Server) dashboardData(window time.Duration) (*dashData, error) {
	now := time.Now().UTC()
	start := now.Add(-window)
	probes, err := s.db.ProbesSince(start)
	if err != nil {
		return nil, err
	}
	// Per-target uptime, time-weighted over every row (normal and fast).
	fp := s.cfg.Monitoring.FastPoll
	ups := report.WeightedUptime(probes, report.Cadence{
		Normal: s.cfg.Monitoring.ProbeInterval(), Fast: fp.Interval(), Slow: fp.SlowInterval(),
	})
	tIdx := map[string]int{}
	targets := make([]*dashTarget, 0, len(ups))
	for i, u := range ups {
		tIdx[u.Target] = i
		targets = append(targets, &dashTarget{Target: u.Target, Kind: u.Kind, Count: u.Count, SuccessCount: u.SuccessCount,
			UptimePct: u.UptimePct, FastCount: u.FastCount, MonitoredSeconds: u.MonitoredSeconds, UpSeconds: u.UpSeconds})
	}
	// Latency series: normal-cadence rounds only, aligned on unique
	// timestamps (rounds share one). Fast rounds probe a reduced target set
	// at a short timeout, so mixing them in would look like failures.
	idx := map[int64]int{}
	times := []int64{}
	allRounds := map[int64]bool{}
	for _, p := range probes {
		ts := p.Timestamp.UnixMilli()
		allRounds[ts] = true
		if p.NormalizedMode() != probe.ModeNormal {
			continue
		}
		if _, ok := idx[ts]; !ok {
			idx[ts] = len(times)
			times = append(times, ts)
		}
	}
	series := make([][]*float64, len(targets))
	for i := range series {
		series[i] = make([]*float64, len(times))
	}
	for _, p := range probes {
		if p.NormalizedMode() != probe.ModeNormal || !p.Success {
			continue
		}
		v := float64(p.Latency.Microseconds()) / 1000.0
		series[tIdx[p.Target]][idx[p.Timestamp.UnixMilli()]] = &v
	}
	fastWindows := report.FastWindows(probes)
	incidents, err := s.db.Incidents(time.Unix(0, 0), now, 200)
	if err != nil {
		return nil, err
	}
	outs, err := s.db.Outages(time.Unix(0, 0), now)
	if err != nil {
		return nil, err
	}
	gaps, err := s.db.Gaps(time.Unix(0, 0), now)
	if err != nil {
		return nil, err
	}
	if incidents == nil {
		incidents = []incident.Event{}
	}
	if outs == nil {
		outs = []storage.Outage{}
	}
	if gaps == nil {
		gaps = []storage.Gap{}
	}
	normalRounds := len(times)
	times, series = downsample(times, series, dashboardMaxPoints)
	return &dashData{
		Window: window.String(), Source: "sqlite", RawRounds: len(allRounds), Points: len(times),
		Start: start, End: now, TimesMS: times, Targets: targets, Latency: series,
		Incidents: incidents, Outages: outs, Gaps: gaps,
		FastRounds: len(allRounds) - normalRounds, FastWindows: fastWindows,
	}, nil
}

func (s *Server) handleReport(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.db == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
			return
		}
		now := time.Now()
		var rep report.Report
		var err error
		if kind == "daily" {
			date := r.URL.Query().Get("date")
			if date == "" {
				date = now.In(report.Location).Format("2006-01-02")
			}
			rep, err = report.Daily(s.db, date, now)
		} else {
			month := r.URL.Query().Get("month")
			if month == "" {
				month = now.In(report.Location).Format("2006-01")
			}
			rep, err = report.Monthly(s.db, month, now)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=fcomt-%s-%s.csv", rep.Kind, rep.Period))
			_ = report.WriteCSV(w, rep)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

func parseWindow(raw string) (time.Duration, error) {
	if raw == "" {
		return 24 * time.Hour, nil
	}
	d, err := ParseWindowDuration(raw)
	if err != nil || d <= 0 {
		return 0, errors.New("window must be a positive duration like 5m, 1h, 24h, 168h, or 7d")
	}
	return d, nil
}

// ParseWindowDuration is time.ParseDuration plus a day unit: "7d", "1.5d",
// and mixed forms like "1d12h" are accepted.
func ParseWindowDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, 'd'); i > 0 {
		days, err := strconv.ParseFloat(raw[:i], 64)
		if err != nil {
			return 0, err
		}
		total := time.Duration(days * float64(24*time.Hour))
		if rest := raw[i+1:]; rest != "" {
			r, err := time.ParseDuration(rest)
			if err != nil {
				return 0, err
			}
			total += r
		}
		return total, nil
	}
	return time.ParseDuration(raw)
}

// dashboardMaxPoints caps the number of x points returned by /v1/dashboard.
const dashboardMaxPoints = 500

// downsample buckets aligned rounds into at most max buckets. Each bucket
// takes its first timestamp and, per target, the max latency; a bucket is
// null (failed) if any probe of that target in it failed.
func downsample(times []int64, series [][]*float64, max int) ([]int64, [][]*float64) {
	n := len(times)
	if n <= max || max <= 0 {
		return times, series
	}
	per := (n + max - 1) / max
	outT := make([]int64, 0, max)
	outS := make([][]*float64, len(series))
	for b := 0; b*per < n; b++ {
		lo, hi := b*per, (b+1)*per
		if hi > n {
			hi = n
		}
		outT = append(outT, times[lo])
		for si, ser := range series {
			var best *float64
			failed := false
			for _, v := range ser[lo:hi] {
				if v == nil {
					failed = true
					break
				}
				if best == nil || *v > *best {
					x := *v
					best = &x
				}
			}
			if failed {
				best = nil
			}
			outS[si] = append(outS[si], best)
		}
	}
	return outT, outS
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fsSub(fsys fs.FS, dir string) (fs.FS, error) {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return fsys, err
	}
	return sub, nil
}
