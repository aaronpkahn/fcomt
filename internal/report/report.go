// Package report builds daily and monthly outage reports from the database.
package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/aaron/fcomt-agent/internal/storage"
)

// Location is the timezone used for day/month boundaries.
var Location = mustLoad("America/Los_Angeles")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

type OutageRow struct {
	Start           time.Time  `json:"start"`
	End             *time.Time `json:"end,omitempty"`
	Ongoing         bool       `json:"ongoing"`
	DurationSeconds float64    `json:"duration_seconds"`
	Cause           string     `json:"cause"`
}

type GapRow struct {
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	DurationSeconds float64   `json:"duration_seconds"`
	Reason          string    `json:"reason"`
}

type Report struct {
	Kind                 string      `json:"kind"`   // daily | monthly
	Period               string      `json:"period"` // 2026-10-25 or 2026-10
	Timezone             string      `json:"timezone"`
	Start                time.Time   `json:"start"`
	End                  time.Time   `json:"end"`
	MonitoredSeconds     float64     `json:"monitored_seconds"`
	UnmonitoredSeconds   float64     `json:"unmonitored_seconds"` // gaps + time before first/after last data
	GapSeconds           float64     `json:"gap_seconds"`
	DowntimeSeconds      float64     `json:"downtime_seconds"`
	ISPDowntimeSeconds   float64     `json:"isp_downtime_seconds"`
	LocalDowntimeSeconds float64     `json:"local_downtime_seconds"`
	UptimePct            float64     `json:"uptime_pct"`
	OutageCount          int         `json:"outage_count"`
	Outages              []OutageRow `json:"outages"`
	GapCount             int         `json:"gap_count"`
	Gaps                 []GapRow    `json:"gaps"`
}

// DayRange returns the local-midnight bounds for date "YYYY-MM-DD".
func DayRange(date string) (time.Time, time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, Location)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}
	return d, d.AddDate(0, 0, 1), nil
}

// MonthRange returns the local bounds for month "YYYY-MM".
func MonthRange(month string) (time.Time, time.Time, error) {
	d, err := time.ParseInLocation("2006-01", month, Location)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("month must be YYYY-MM: %w", err)
	}
	return d, d.AddDate(0, 1, 0), nil
}

func Daily(db *storage.DB, date string, now time.Time) (Report, error) {
	s, e, err := DayRange(date)
	if err != nil {
		return Report{}, err
	}
	return Build(db, "daily", date, s, e, now)
}

func Monthly(db *storage.DB, month string, now time.Time) (Report, error) {
	s, e, err := MonthRange(month)
	if err != nil {
		return Report{}, err
	}
	return Build(db, "monthly", month, s, e, now)
}

func overlap(aStart, aEnd, bStart, bEnd time.Time) time.Duration {
	s, e := aStart, aEnd
	if bStart.After(s) {
		s = bStart
	}
	if bEnd.Before(e) {
		e = bEnd
	}
	if e.After(s) {
		return e.Sub(s)
	}
	return 0
}

// Build computes a report for [start, end). Uptime excludes unmonitored time
// (gaps, and any part of the period before the first probe or after now).
func Build(db *storage.DB, kind, period string, start, end, now time.Time) (Report, error) {
	r := Report{Kind: kind, Period: period, Timezone: Location.String(), Start: start, End: end,
		Outages: []OutageRow{}, Gaps: []GapRow{}}

	// The window that could have been monitored at all.
	effStart, effEnd := start, end
	if now.Before(effEnd) {
		effEnd = now
	}
	first, ok, err := db.FirstProbeTime()
	if err != nil {
		return r, err
	}
	if !ok {
		effEnd = effStart
	} else if first.After(effStart) {
		effStart = first
	}
	if effEnd.Before(effStart) {
		effEnd = effStart
	}

	gaps, err := db.Gaps(start, end)
	if err != nil {
		return r, err
	}
	var gapDur time.Duration
	for _, g := range gaps {
		gapDur += overlap(g.Start, g.End, effStart, effEnd)
		r.Gaps = append(r.Gaps, GapRow{Start: g.Start.In(Location), End: g.End.In(Location),
			DurationSeconds: g.End.Sub(g.Start).Seconds(), Reason: g.Reason})
	}
	r.GapCount = len(r.Gaps)
	r.GapSeconds = gapDur.Seconds()

	outs, err := db.Outages(start, end)
	if err != nil {
		return r, err
	}
	var down time.Duration
	for _, o := range outs {
		oEnd := now
		if o.End != nil {
			oEnd = *o.End
		}
		d := overlap(o.Start, oEnd, effStart, effEnd)
		// Don't count downtime that overlaps a gap (we weren't watching).
		for _, g := range gaps {
			gs, ge := g.Start, g.End
			if gs.Before(o.Start) {
				gs = o.Start
			}
			if ge.After(oEnd) {
				ge = oEnd
			}
			if ge.After(gs) {
				d -= overlap(gs, ge, effStart, effEnd)
			}
		}
		if d < 0 {
			d = 0
		}
		down += d
		if o.Cause == "local" {
			r.LocalDowntimeSeconds += d.Seconds()
		} else {
			r.ISPDowntimeSeconds += d.Seconds()
		}
		row := OutageRow{Start: o.Start.In(Location), Ongoing: o.End == nil, DurationSeconds: o.Duration(now).Seconds(), Cause: o.Cause}
		if o.End != nil {
			e := o.End.In(Location)
			row.End = &e
		}
		r.Outages = append(r.Outages, row)
	}
	r.OutageCount = len(r.Outages)
	r.DowntimeSeconds = down.Seconds()

	monitored := effEnd.Sub(effStart) - gapDur
	if monitored < 0 {
		monitored = 0
	}
	r.MonitoredSeconds = monitored.Seconds()
	r.UnmonitoredSeconds = end.Sub(start).Seconds() - r.MonitoredSeconds
	if monitored > 0 {
		r.UptimePct = 100 * (monitored - down).Seconds() / monitored.Seconds()
	}
	return r, nil
}

// WriteCSV writes a summary header block followed by one row per outage and gap.
func WriteCSV(w io.Writer, r Report) error {
	cw := csv.NewWriter(w)
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	ts := func(t time.Time) string { return t.In(Location).Format(time.RFC3339) }
	rows := [][]string{
		{"kind", "period", "timezone", "uptime_pct", "outage_count", "downtime_seconds", "isp_downtime_seconds", "local_downtime_seconds", "gap_count", "gap_seconds", "monitored_seconds"},
		{r.Kind, r.Period, r.Timezone, f(r.UptimePct), strconv.Itoa(r.OutageCount), f(r.DowntimeSeconds), f(r.ISPDowntimeSeconds), f(r.LocalDowntimeSeconds), strconv.Itoa(r.GapCount), f(r.GapSeconds), f(r.MonitoredSeconds)},
		{},
		{"type", "start", "end", "duration_seconds", "cause_or_reason"},
	}
	for _, o := range r.Outages {
		end := "ongoing"
		if o.End != nil {
			end = ts(*o.End)
		}
		rows = append(rows, []string{"outage", ts(o.Start), end, f(o.DurationSeconds), o.Cause})
	}
	for _, g := range r.Gaps {
		rows = append(rows, []string{"gap", ts(g.Start), ts(g.End), f(g.DurationSeconds), g.Reason})
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}
