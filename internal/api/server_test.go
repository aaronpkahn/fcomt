package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
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
