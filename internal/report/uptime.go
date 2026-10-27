package report

import (
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

// Cadence is the nominal spacing of rows by polling mode.
type Cadence struct {
	Normal time.Duration
	Fast   time.Duration
	Slow   time.Duration
}

func (c Cadence) nominal(mode string) time.Duration {
	switch mode {
	case probe.ModeFast:
		if c.Fast > 0 {
			return c.Fast
		}
	case probe.ModeSlow:
		if c.Slow > 0 {
			return c.Slow
		}
	}
	return c.Normal
}

// TargetUptime is a per-target availability summary.
type TargetUptime struct {
	Target       string
	Kind         string
	Count        int // raw rows, all modes
	SuccessCount int // raw successful rows, all modes
	FastCount    int // rows taken in fast/slow mode
	// MonitoredSeconds / UpSeconds are time-weighted: each row stands for the
	// time until that target's next row, capped at the normal interval (so a
	// sleep or restart gap isn't credited), and the last row for its mode's
	// nominal interval. A 2s fast-poll row therefore counts 1/15 of a 30s row.
	MonitoredSeconds float64
	UpSeconds        float64
	UptimePct        float64
}

// WeightedUptime computes time-weighted uptime per target from probes sorted
// by time. Targets are returned in order of first appearance.
func WeightedUptime(probes []probe.Result, c Cadence) []TargetUptime {
	if c.Normal <= 0 {
		c.Normal = 30 * time.Second
	}
	idx := map[string]int{}
	var out []TargetUptime
	byTarget := map[string][]probe.Result{}
	for _, p := range probes {
		if _, ok := idx[p.Target]; !ok {
			idx[p.Target] = len(out)
			out = append(out, TargetUptime{Target: p.Target, Kind: string(p.Kind)})
		}
		byTarget[p.Target] = append(byTarget[p.Target], p)
	}
	for name, rows := range byTarget {
		t := &out[idx[name]]
		var mon, up time.Duration
		for i, p := range rows {
			t.Count++
			if p.Success {
				t.SuccessCount++
			}
			if p.NormalizedMode() != probe.ModeNormal {
				t.FastCount++
			}
			w := c.nominal(p.NormalizedMode())
			if i+1 < len(rows) {
				w = rows[i+1].Timestamp.Sub(p.Timestamp)
			}
			if w > c.Normal {
				w = c.Normal
			}
			if w < 0 {
				w = 0
			}
			mon += w
			if p.Success {
				up += w
			}
		}
		t.MonitoredSeconds, t.UpSeconds = mon.Seconds(), up.Seconds()
		if mon > 0 {
			t.UptimePct = 100 * t.UpSeconds / t.MonitoredSeconds
		} else if t.Count > 0 {
			t.UptimePct = 100 * float64(t.SuccessCount) / float64(t.Count)
		}
	}
	return out
}

// FastWindow is a contiguous run of fast/slow-mode rounds.
type FastWindow struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Rounds int       `json:"rounds"`
	Capped bool      `json:"capped"` // reached the cap and dropped to the slow cadence
}

// FastWindows groups consecutive non-normal rounds (by timestamp) from probes
// sorted by time.
func FastWindows(probes []probe.Result) []FastWindow {
	out := []FastWindow{}
	var cur *FastWindow
	var lastTS time.Time
	for _, p := range probes {
		if p.Timestamp.Equal(lastTS) && !lastTS.IsZero() {
			continue // same round
		}
		lastTS = p.Timestamp
		mode := p.NormalizedMode()
		if mode == probe.ModeNormal {
			cur = nil
			continue
		}
		if cur == nil {
			out = append(out, FastWindow{Start: p.Timestamp})
			cur = &out[len(out)-1]
		}
		cur.End = p.Timestamp
		cur.Rounds++
		if mode == probe.ModeSlow {
			cur.Capped = true
		}
	}
	return out
}
