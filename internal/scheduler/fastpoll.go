package scheduler

import (
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/probe"
)

// FastPoll is the fast-poll-on-failure state machine. It is fed one
// observation per round (was any external destination failing?) and decides
// the mode, and therefore the cadence, of the next round:
//
//	normal --(any external failure)--> fast
//	fast   --(window open >= MaxFast)--> slow
//	fast|slow --(all OK for >= Tail since first OK round)--> normal
//
// A failure during the tail restarts the tail. The cap is measured from the
// start of the window, so a flapping link drops to the slow cadence after
// MaxFast and stays there until it has been stable for Tail.
type FastPoll struct {
	Enabled bool
	Normal  time.Duration // regular cadence (30s)
	Fast    time.Duration // 2s
	Slow    time.Duration // 10s, after the cap
	Tail    time.Duration // 60s of all-OK before returning to normal
	MaxFast time.Duration // 10 min cap on the fast cadence

	mode    string
	start   time.Time // when the window opened
	okSince time.Time // first all-OK round of the current recovery; zero while failing
}

// Transition reports a mode change produced by Observe.
type FastPollTransition struct {
	From, To string
	At       time.Time
	Window   time.Duration // how long the window had been open (when leaving fast/slow)
}

func NewFastPoll(normal time.Duration, fp config.FastPollConfig) *FastPoll {
	f := &FastPoll{
		Enabled: fp.Enabled,
		Normal:  normal,
		Fast:    fp.Interval(),
		Slow:    fp.SlowInterval(),
		Tail:    fp.Tail(),
		MaxFast: fp.MaxFast(),
		mode:    probe.ModeNormal,
	}
	// Fast polling only makes sense if it is actually faster than normal
	// (server_assisted already probes every 500ms).
	if f.Fast <= 0 || f.Fast >= normal {
		f.Enabled = false
	}
	if f.Slow < f.Fast {
		f.Slow = f.Fast
	}
	return f
}

// Mode returns the mode the next round should run in.
func (f *FastPoll) Mode() string {
	if f.mode == "" {
		return probe.ModeNormal
	}
	return f.mode
}

// Interval returns the delay until the next round.
func (f *FastPoll) Interval() time.Duration {
	switch f.Mode() {
	case probe.ModeFast:
		return f.Fast
	case probe.ModeSlow:
		return f.Slow
	}
	return f.Normal
}

// Reset returns to normal mode (e.g. after the machine slept).
func (f *FastPoll) Reset() {
	f.mode, f.start, f.okSince = probe.ModeNormal, time.Time{}, time.Time{}
}

// Observe records the outcome of the round taken at now and returns the
// resulting transition (zero value if the mode did not change).
func (f *FastPoll) Observe(now time.Time, failed bool) (FastPollTransition, bool) {
	from := f.Mode()
	if !f.Enabled {
		return FastPollTransition{}, false
	}
	switch from {
	case probe.ModeNormal:
		if failed {
			f.mode, f.start, f.okSince = probe.ModeFast, now, time.Time{}
		}
	default:
		if failed {
			f.okSince = time.Time{}
		} else if f.okSince.IsZero() {
			f.okSince = now
		}
		switch {
		case !f.okSince.IsZero() && now.Sub(f.okSince) >= f.Tail:
			w := now.Sub(f.start)
			f.Reset()
			return FastPollTransition{From: from, To: probe.ModeNormal, At: now, Window: w}, true
		case from == probe.ModeFast && now.Sub(f.start) >= f.MaxFast:
			f.mode = probe.ModeSlow
		}
	}
	if to := f.Mode(); to != from {
		return FastPollTransition{From: from, To: to, At: now, Window: now.Sub(f.start)}, true
	}
	return FastPollTransition{}, false
}

// RoundFailed reports whether any external destination failed in the round.
func RoundFailed(results []probe.Result) bool {
	for _, r := range results {
		if r.Kind.IsExternal() && !r.Success {
			return true
		}
	}
	return false
}

// FastTargets reduces targets to the gateway plus the named destinations.
// If none of the named destinations exist it returns all targets so the
// window can still detect recovery.
func FastTargets(all []probe.Target, names []string) []probe.Target {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := make([]probe.Target, 0, len(names)+1)
	external := 0
	for _, t := range all {
		switch {
		case t.Kind == probe.KindGateway:
			out = append(out, t)
		case want[t.Name]:
			out = append(out, t)
			external++
		}
	}
	if external == 0 {
		return all
	}
	return out
}

// CadenceSampler passes through rounds at no more than the normal cadence, so
// consumers that count rounds (the in-memory collector, the per-target
// incident evaluator with its consecutive-round threshold) see the same rate
// whether or not a fast-poll window is open. Normal rounds always pass.
type CadenceSampler struct {
	Interval time.Duration
	last     time.Time
}

// Allow reports whether a round of the given mode at t should be sampled.
func (c *CadenceSampler) Allow(t time.Time, mode string) bool {
	// Allow a little jitter so a fast round ~30s after the last sample counts.
	slack := c.Interval / 20
	if mode == "" || mode == probe.ModeNormal || c.last.IsZero() || t.Sub(c.last) >= c.Interval-slack {
		c.last = t
		return true
	}
	return false
}
