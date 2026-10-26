package storage

import (
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
