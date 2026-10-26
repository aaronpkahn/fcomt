package outage

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

func round(gw bool, ext ...bool) []probe.Result {
	out := []probe.Result{{Target: "gw", Kind: probe.KindGateway, Success: gw}}
	for _, ok := range ext {
		out = append(out, probe.Result{Target: "x", Kind: probe.KindDestination, Success: ok})
	}
	return out
}

func TestDetectorISPAndLocal(t *testing.T) {
	var d Detector
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if tr := d.Observe(t0, round(true, true, false, true)); tr.Opened {
		t.Fatal("partial failure must not open")
	}
	tr := d.Observe(t0.Add(30*time.Second), round(true, false, false, false))
	if !tr.Opened || tr.Cause != CauseISP {
		t.Fatalf("expected isp outage, got %+v", tr)
	}
	if tr := d.Observe(t0.Add(60*time.Second), round(true, false, false, false)); tr.Opened || tr.Closed {
		t.Fatal("still down, no transition expected")
	}
	tr = d.Observe(t0.Add(90*time.Second), round(true, false, true, false))
	if !tr.Closed || tr.End.Sub(tr.Start) != time.Minute {
		t.Fatalf("expected close after 1m, got %+v", tr)
	}
	tr = d.Observe(t0.Add(120*time.Second), round(false, false, false, false))
	if !tr.Opened || tr.Cause != CauseLocal {
		t.Fatalf("expected local outage, got %+v", tr)
	}
}
