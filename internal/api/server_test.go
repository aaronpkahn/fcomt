package api

import (
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/storage"
	"strings"
)

func TestSummaryEndpoint(t *testing.T) {
	cfg := config.Default()
	collector := metrics.NewCollector(time.Hour)
	store := incident.NewStore(100)
	srv := New(cfg, collector, store)

	now := time.Now().UTC()
	collector.Add(probe.Result{Target: "google", Timestamp: now.Add(-1 * time.Minute), Success: true, Latency: 10 * time.Millisecond})
	collector.Add(probe.Result{Target: "google", Timestamp: now.Add(-30 * time.Second), Success: false})

	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/summary?window=5m&target=google", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var summary metrics.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.Target != "google" {
		t.Fatalf("unexpected target: %s", summary.Target)
	}
	if summary.Count != 2 {
		t.Fatalf("expected 2 samples, got %d", summary.Count)
	}
}

func TestEvidenceEndpoint(t *testing.T) {
	cfg := config.Default()
	collector := metrics.NewCollector(time.Hour)
	store := incident.NewStore(100)
	srv := New(cfg, collector, store)

	req := httptest.NewRequest(http.MethodGet, "/v1/export/evidence?window=24h", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTimeSeriesEndpoint(t *testing.T) {
	cfg := config.Default()
	collector := metrics.NewCollector(time.Hour)
	store := incident.NewStore(100)
	srv := New(cfg, collector, store)

	now := time.Now().UTC()
	collector.Add(probe.Result{Target: "google", Timestamp: now.Add(-2 * time.Hour), Success: true, Latency: 15 * time.Millisecond})
	collector.Add(probe.Result{Target: "google", Timestamp: now.Add(-1 * time.Hour), Success: false, Error: "timeout"})

	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/timeseries?window=24h&target=google", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	series, ok := payload["series"].([]any)
	if !ok || len(series) == 0 {
		t.Fatalf("expected non-empty series payload")
	}
}

func TestExampleEndpoints(t *testing.T) {
	cfg := config.Default()
	cfg.API.EnableDemo = true
	collector := metrics.NewCollector(time.Hour)
	store := incident.NewStore(100)
	srv := New(cfg, collector, store)

	listReq := httptest.NewRequest(http.MethodGet, "/v1/examples", nil)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for examples list, got %d", listRec.Code)
	}

	var scenarios []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &scenarios); err != nil {
		t.Fatalf("unmarshal scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("expected at least one scenario")
	}

	name, _ := scenarios[0]["name"].(string)
	if name == "" {
		t.Fatalf("scenario missing name")
	}

	seedReq := httptest.NewRequest(http.MethodPost, "/v1/examples/seed?name="+name, nil)
	seedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seedRec, seedReq)
	if seedRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for seed, got %d", seedRec.Code)
	}

	latestReq := httptest.NewRequest(http.MethodGet, "/v1/metrics/latest", nil)
	latestRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(latestRec, latestReq)
	if latestRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for latest metrics after seed, got %d", latestRec.Code)
	}
}

func TestUIRoutes(t *testing.T) {
	cfg := config.Default()
	collector := metrics.NewCollector(time.Hour)
	store := incident.NewStore(100)
	srv := New(cfg, collector, store)

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected redirect from /, got %d", rootRec.Code)
	}

	uiReq := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	uiRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(uiRec, uiReq)
	if uiRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /ui/, got %d", uiRec.Code)
	}
}

func TestSeedDisabledByDefault(t *testing.T) {
	srv := New(config.Default(), metrics.NewCollector(time.Hour), incident.NewStore(10))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/examples/seed?name=x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestEvidenceFiltersWindow(t *testing.T) {
	store := incident.NewStore(10)
	now := time.Now().UTC()
	store.Add(incident.Event{Time: now.Add(-48 * time.Hour), Target: "old"})
	store.Add(incident.Event{Time: now.Add(-time.Minute), Target: "new"})
	srv := New(config.Default(), metrics.NewCollector(time.Hour), store)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/export/evidence?window=1h", nil))
	var b struct {
		Incidents []incident.Event `json:"incidents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if len(b.Incidents) != 1 || b.Incidents[0].Target != "new" {
		t.Fatalf("expected only in-window incident, got %+v", b.Incidents)
	}
}

func TestReportEndpoints(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv := New(config.Default(), metrics.NewCollector(time.Hour), incident.NewStore(10))
	srv.SetDB(db)
	for _, u := range []string{"/v1/reports/daily?date=2026-10-20", "/v1/reports/monthly?month=2026-10", "/v1/outages?window=24h"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d %s", u, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/reports/daily?date=2026-10-20&format=csv", nil))
	if !strings.HasPrefix(rec.Body.String(), "kind,period") {
		t.Fatalf("unexpected csv: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/reports/daily?date=bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestDashboardEndpoint(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Add(-time.Minute)
	_ = db.InsertProbes([]probe.Result{
		{Target: "a", Kind: probe.KindDestination, Timestamp: now, Success: true, Latency: 10 * time.Millisecond},
		{Target: "b", Kind: probe.KindDestination, Timestamp: now, Success: false},
	})
	srv := New(config.Default(), metrics.NewCollector(time.Hour), incident.NewStore(10))
	srv.SetDB(db)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/dashboard?window=1h", nil))
	var d struct {
		Times   []int64      `json:"times_ms"`
		Latency [][]*float64 `json:"latency"`
		Targets []struct {
			UptimePct float64 `json:"uptime_pct"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Times) != 1 || len(d.Latency) != 2 || d.Latency[0][0] == nil || *d.Latency[0][0] != 10 || d.Latency[1][0] != nil {
		t.Fatalf("unexpected dashboard: %s", rec.Body.String())
	}
	if d.Targets[0].UptimePct != 100 || d.Targets[1].UptimePct != 0 {
		t.Fatalf("uptime: %+v", d.Targets)
	}
}

func TestParseWindowDays(t *testing.T) {
	cases := map[string]time.Duration{"7d": 168 * time.Hour, "1d": 24 * time.Hour, "168h": 168 * time.Hour, "1d12h": 36 * time.Hour, "0.5d": 12 * time.Hour, "": 24 * time.Hour}
	for in, want := range cases {
		got, err := parseWindow(in)
		if err != nil || got != want {
			t.Errorf("parseWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"d", "xd", "-1d", "7days"} {
		if _, err := parseWindow(bad); err == nil {
			t.Errorf("parseWindow(%q) should fail", bad)
		}
	}
}

func TestDownsample(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	times := make([]int64, 1000)
	ser := make([]*float64, 1000)
	for i := range times {
		times[i] = int64(i)
		ser[i] = f(float64(i % 7))
	}
	ser[5] = nil
	ot, os := downsample(times, [][]*float64{ser}, 500)
	if len(ot) != 500 || len(os[0]) != 500 {
		t.Fatalf("len %d", len(ot))
	}
	if os[0][2] != nil { // bucket [4,5] contains a failure
		t.Fatalf("bucket with failure should be null")
	}
	if *os[0][3] != 6 { // bucket [6,7] -> max(6,0)
		t.Fatalf("max = %v", *os[0][3])
	}
}

func TestDashboard7dFromSQLite(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := time.Now().UTC().Add(-6 * 24 * time.Hour)
	var rs []probe.Result
	for i := 0; i < 2000; i++ {
		rs = append(rs, probe.Result{Target: "a", Kind: probe.KindDestination, Timestamp: old.Add(time.Duration(i) * 30 * time.Second), Success: i != 10, Latency: time.Millisecond})
	}
	if err := db.InsertProbes(rs); err != nil {
		t.Fatal(err)
	}
	// Empty in-memory collector: data must come from SQLite.
	srv := New(config.Default(), metrics.NewCollector(time.Hour), incident.NewStore(10))
	srv.SetDB(db)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/dashboard?window=7d", nil))
	var d struct {
		Points    int     `json:"points"`
		RawRounds int     `json:"raw_rounds"`
		Times     []int64 `json:"times_ms"`
		Targets   []struct {
			Count     int     `json:"count"`
			UptimePct float64 `json:"uptime_pct"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(rec.Body.String())
	}
	if d.RawRounds != 2000 || d.Points > 500 || len(d.Times) != d.Points || d.Times[0] != old.UnixMilli() {
		t.Fatalf("raw=%d points=%d first=%v", d.RawRounds, d.Points, d.Times)
	}
	if d.Targets[0].Count != 2000 || d.Targets[0].UptimePct != 100*1999.0/2000 {
		t.Fatalf("uptime must use all probes: %+v", d.Targets)
	}
}

func TestChartPNG(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv := New(config.Default(), metrics.NewCollector(time.Hour), incident.NewStore(10))
	srv.SetDB(db)
	get := func(q string) image.Image {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chart.png"+q, nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%s: %d %s %s", q, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		img, err := png.Decode(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	// Empty DB renders a valid placeholder chart.
	if b := get("").Bounds(); b.Dx() != 1200 || b.Dy() != 800 {
		t.Fatalf("size %v", b)
	}
	now := time.Now().UTC().Add(-2 * time.Hour)
	var rs []probe.Result
	for i := 0; i < 100; i++ {
		ts := now.Add(time.Duration(i) * 30 * time.Second)
		rs = append(rs, probe.Result{Target: "google-dns", Kind: probe.KindDestination, Timestamp: ts, Success: i%10 != 0, Latency: 20 * time.Millisecond},
			probe.Result{Target: "gateway", Kind: probe.KindGateway, Timestamp: ts, Success: true, Latency: 3 * time.Millisecond})
	}
	if err := db.InsertProbes(rs); err != nil {
		t.Fatal(err)
	}
	id, _ := db.OpenOutage(now.Add(10*time.Minute), "isp")
	_ = db.CloseOutage(id, now.Add(12*time.Minute))
	_ = db.InsertGap(storage.Gap{Start: now.Add(20 * time.Minute), End: now.Add(25 * time.Minute), Reason: "sleep"})
	if p := os.Getenv("CHART_OUT"); p != "" {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chart.png?window=7d", nil))
		_ = os.WriteFile(p, rec.Body.Bytes(), 0o644)
	}
	get("?window=7d")
	get("?window=1h")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chart.png?window=bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", rec.Code)
	}
}
