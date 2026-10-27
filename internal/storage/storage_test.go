package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/probe"
)

func TestPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "fcomt.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.InsertProbes([]probe.Result{{Target: "g", Kind: probe.KindDestination, Timestamp: now, Latency: 12 * time.Millisecond, Success: true}}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertIncident(incident.Event{Time: now, Target: "g", State: "open", Severity: "critical", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenOutage(now, "isp"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ps, _ := db.ProbesSince(now.Add(-time.Hour))
	if len(ps) != 1 || ps[0].Latency != 12*time.Millisecond || !ps[0].Success {
		t.Fatalf("probes not persisted: %+v", ps)
	}
	last, ok, _ := db.LastProbeTime()
	if !ok || !last.Equal(now) {
		t.Fatalf("last probe %v %v", last, ok)
	}
	inc, _ := db.Incidents(now.Add(-time.Hour), now.Add(time.Hour), 0)
	if len(inc) != 1 {
		t.Fatalf("incidents %+v", inc)
	}
	if n, _ := db.CloseDanglingOutages(now.Add(time.Minute)); n != 1 {
		t.Fatalf("expected 1 dangling outage closed, got %d", n)
	}
	outs, _ := db.Outages(now.Add(-time.Hour), now.Add(time.Hour))
	if len(outs) != 1 || outs[0].End == nil {
		t.Fatalf("outage %+v", outs)
	}
}

// An existing DB created before the mode column must be migrated in place,
// idempotently, with old rows reading as "normal".
func TestMigrateAddsModeColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE probes (ts_ms INTEGER NOT NULL, target TEXT NOT NULL, kind TEXT NOT NULL,
		latency_us INTEGER NOT NULL, success INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '');
		INSERT INTO probes(ts_ms,target,kind,latency_us,success,error) VALUES (1000,'google-dns','destination',28000,1,'')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	for i := 0; i < 2; i++ { // second Open must be a no-op
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		if ok, _ := db.HasColumn("probes", "mode"); !ok {
			t.Fatal("mode column missing")
		}
		db.Close()
	}
	db, _ := Open(path)
	defer db.Close()
	ts := time.UnixMilli(2000).UTC()
	if err := db.InsertProbes([]probe.Result{{Target: "cloudflare-dns", Kind: probe.KindDestination, Timestamp: ts, Mode: probe.ModeFast}}); err != nil {
		t.Fatal(err)
	}
	ps, _ := db.ProbesSince(time.UnixMilli(0))
	if len(ps) != 2 || ps[0].Mode != probe.ModeNormal || ps[1].Mode != probe.ModeFast {
		t.Fatalf("modes after migration: %+v", ps)
	}
	// An old binary's INSERT (no mode column) still works and defaults to normal.
	raw, _ = sql.Open("sqlite", "file:"+path)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO probes(ts_ms,target,kind,latency_us,success,error) VALUES (3000,'g','destination',1,1,'')`); err != nil {
		t.Fatal(err)
	}
	var mode string
	_ = raw.QueryRow(`SELECT mode FROM probes WHERE ts_ms=3000`).Scan(&mode)
	if mode != "normal" {
		t.Fatalf("default mode %q", mode)
	}
}
