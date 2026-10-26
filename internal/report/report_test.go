package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
	"github.com/aaron/fcomt-agent/internal/storage"
)

func TestDailyReport(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	day, _, _ := DayRange("2026-10-20")
	// Data from local midnight.
	if err := db.InsertProbes([]probe.Result{{Target: "g", Kind: probe.KindDestination, Timestamp: day, Success: true}}); err != nil {
		t.Fatal(err)
	}
	id, _ := db.OpenOutage(day.Add(2*time.Hour), "isp")
	_ = db.CloseOutage(id, day.Add(2*time.Hour+10*time.Minute))
	_ = db.InsertGap(storage.Gap{Start: day.Add(10 * time.Hour), End: day.Add(12 * time.Hour), Reason: "sleep"})
	// Outage on the previous local day must not appear.
	id2, _ := db.OpenOutage(day.Add(-time.Hour), "local")
	_ = db.CloseOutage(id2, day.Add(-50*time.Minute))

	r, err := Daily(db, "2026-10-20", day.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r.OutageCount != 1 || r.DowntimeSeconds != 600 {
		t.Fatalf("unexpected outages: %+v", r)
	}
	if r.GapSeconds != 7200 || r.GapCount != 1 {
		t.Fatalf("unexpected gaps: %+v", r)
	}
	wantMon := (22 * time.Hour).Seconds()
	if r.MonitoredSeconds != wantMon {
		t.Fatalf("monitored %v want %v", r.MonitoredSeconds, wantMon)
	}
	want := 100 * (wantMon - 600) / wantMon
	if r.UptimePct != want {
		t.Fatalf("uptime %v want %v", r.UptimePct, want)
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "outage,2026-10-20T02:00:00-07:00") {
		t.Fatalf("csv missing outage row:\n%s", buf.String())
	}
}

func TestMonthRange(t *testing.T) {
	s, e, err := MonthRange("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	// DST ends Nov 1 2026, so November is 30 days + 1 hour long.
	if e.Sub(s) != 30*24*time.Hour+time.Hour {
		t.Fatalf("unexpected month length %v", e.Sub(s))
	}
}
