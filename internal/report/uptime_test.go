package report

import (
	"math"
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

var cad = Cadence{Normal: 30 * time.Second, Fast: 2 * time.Second, Slow: 10 * time.Second}

// A 10s blip seen by fast polling must cost ~10s of downtime, not 6 rows'
// worth of a naive row count.
func TestWeightedUptimeFastRows(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	var ps []probe.Result
	add := func(at time.Duration, ok bool, mode string) {
		ps = append(ps, probe.Result{Target: "cloudflare-dns", Kind: probe.KindDestination, Timestamp: t0.Add(at), Success: ok, Mode: mode})
	}
	// 10 normal OK rounds (0..270s), failing normal round at 300s, then fast
	// rows every 2s: fail through 308s, OK 310..370s, back to normal at 400s.
	for i := 0; i < 10; i++ {
		add(time.Duration(i)*30*time.Second, true, probe.ModeNormal)
	}
	add(300*time.Second, false, probe.ModeNormal)
	for s := 302; s <= 308; s += 2 {
		add(time.Duration(s)*time.Second, false, probe.ModeFast)
	}
	for s := 310; s <= 370; s += 2 {
		add(time.Duration(s)*time.Second, true, probe.ModeFast)
	}
	add(400*time.Second, true, probe.ModeNormal)

	u := WeightedUptime(ps, cad)
	if len(u) != 1 {
		t.Fatal(u)
	}
	got := u[0]
	// Monitored 0..400s plus the last row's 30s = 430s; down 300..310s = 10s.
	if got.MonitoredSeconds != 430 || got.MonitoredSeconds-got.UpSeconds != 10 {
		t.Fatalf("weights: %+v", got)
	}
	if want := 100 * 420.0 / 430; math.Abs(got.UptimePct-want) > 1e-9 {
		t.Fatalf("uptime %.4f want %.4f", got.UptimePct, want)
	}
	naive := 100 * float64(got.SuccessCount) / float64(got.Count)
	if naive > 90 { // row counting would say ~89% because of the dense rows
		t.Fatalf("test setup: naive %.2f", naive)
	}
	if got.FastCount != 35 || got.Count != 47 {
		t.Fatalf("counts %+v", got)
	}
}

func TestWeightedUptimeCapsGaps(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	ps := []probe.Result{
		{Target: "a", Timestamp: t0, Success: false},
		{Target: "a", Timestamp: t0.Add(2 * time.Hour), Success: true}, // machine asleep in between
	}
	u := WeightedUptime(ps, cad)[0]
	if u.MonitoredSeconds != 60 || u.UptimePct != 50 {
		t.Fatalf("gap must be capped at the normal interval: %+v", u)
	}
}

func TestFastWindows(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	mk := func(s int, mode string) []probe.Result {
		ts := t0.Add(time.Duration(s) * time.Second)
		return []probe.Result{{Target: "gateway", Timestamp: ts, Mode: mode}, {Target: "cloudflare-dns", Timestamp: ts, Mode: mode}}
	}
	var ps []probe.Result
	ps = append(ps, mk(0, "")...)
	ps = append(ps, mk(30, probe.ModeNormal)...)
	ps = append(ps, mk(32, probe.ModeFast)...)
	ps = append(ps, mk(34, probe.ModeFast)...)
	ps = append(ps, mk(44, probe.ModeSlow)...)
	ps = append(ps, mk(74, probe.ModeNormal)...)
	ps = append(ps, mk(76, probe.ModeFast)...)
	w := FastWindows(ps)
	if len(w) != 2 || w[0].Rounds != 3 || !w[0].Capped || !w[0].End.Equal(t0.Add(44*time.Second)) || w[1].Rounds != 1 || w[1].Capped {
		t.Fatalf("windows %+v", w)
	}
}
