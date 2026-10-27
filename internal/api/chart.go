package api

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"net/http"
	"strings"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

var chartLoc = func() *time.Location {
	l, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PT", -7*3600)
	}
	return l
}()

var targetColors = map[string]color.RGBA{
	"gateway":        {0x8e, 0x44, 0xad, 0xff},
	"google-dns":     {0x29, 0x80, 0xb9, 0xff},
	"cloudflare-dns": {0xe6, 0x7e, 0x22, 0xff},
	"aws-us-east-1":  {0x27, 0xae, 0x60, 0xff},
}

var fallbackColors = []color.RGBA{{0xc0, 0x39, 0x2b, 0xff}, {0x16, 0xa0, 0x85, 0xff}, {0x2c, 0x3e, 0x50, 0xff}}

// handleChartPNG renders the dashboard data as a PNG in pure Go (gonum/plot).
func (s *Server) handleChartPNG(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	raw := r.URL.Query().Get("window")
	window := 7 * 24 * time.Hour
	if raw != "" {
		d, err := parseWindow(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		window = d
	}
	d, err := s.dashboardData(window)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	png, err := renderChart(d)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func ptTime(t time.Time) string { return t.In(chartLoc).Format("Jan 2 3:04 PM") }

func renderChart(d *dashData) ([]byte, error) {
	p := plot.New()
	end := float64(d.End.Unix())
	start := float64(d.Start.Unix())
	// Zoom to recorded history when it is shorter than the window.
	first := math.Inf(1)
	if len(d.TimesMS) > 0 {
		first = float64(d.TimesMS[0]) / 1000
	}
	for _, o := range d.Outages {
		first = math.Min(first, float64(o.Start.Unix()))
	}
	for _, g := range d.Gaps {
		first = math.Min(first, float64(g.Start.Unix()))
	}
	xmin := start
	if !math.IsInf(first, 1) && first > start {
		xmin = first - 30
	}
	xmax := end

	maxLat := 0.0
	for _, ser := range d.Latency {
		for _, v := range ser {
			if v != nil && *v > maxLat {
				maxLat = *v
			}
		}
	}
	ymax := 100.0
	if maxLat > 0 {
		ymax = maxLat * 1.15
	}

	// Shaded bands first so lines draw on top.
	band := func(a, b float64, c color.Color) error {
		a, b = math.Max(a, xmin), math.Min(b, xmax)
		if b <= a {
			return nil
		}
		poly, err := plotter.NewPolygon(plotter.XYs{{X: a, Y: 0}, {X: b, Y: 0}, {X: b, Y: ymax}, {X: a, Y: ymax}})
		if err != nil {
			return err
		}
		poly.Color = c
		poly.LineStyle.Width = 0
		p.Add(poly)
		return nil
	}
	for _, g := range d.Gaps {
		if err := band(float64(g.Start.Unix()), float64(g.End.Unix()), color.NRGBA{0x99, 0x99, 0x99, 0x55}); err != nil {
			return nil, err
		}
	}
	for _, f := range d.FastWindows {
		// Pad zero-length (single-round) windows so they remain visible.
		if err := band(float64(f.Start.Unix())-1, float64(f.End.Unix())+1, color.NRGBA{0x34, 0x98, 0xdb, 0x40}); err != nil {
			return nil, err
		}
	}
	for _, o := range d.Outages {
		e := d.End
		if o.End != nil {
			e = *o.End
		}
		if err := band(float64(o.Start.Unix()), float64(e.Unix()), color.NRGBA{0xe7, 0x4c, 0x3c, 0x55}); err != nil {
			return nil, err
		}
	}

	// One line per target, split into segments at failed (null) points.
	for i, t := range d.Targets {
		c, ok := targetColors[t.Target]
		if !ok {
			c = fallbackColors[i%len(fallbackColors)]
		}
		style := draw.LineStyle{Color: c, Width: vg.Points(1.6)}
		var seg plotter.XYs
		flush := func() error {
			if len(seg) == 0 {
				return nil
			}
			if len(seg) == 1 { // lone point: draw a tiny horizontal tick so it stays visible
				seg = append(seg, plotter.XY{X: seg[0].X + 1, Y: seg[0].Y})
			}
			l, err := plotter.NewLine(seg)
			if err != nil {
				return err
			}
			l.LineStyle = style
			p.Add(l)
			seg = nil
			return nil
		}
		if i < len(d.Latency) {
			for j, v := range d.Latency[i] {
				if v == nil {
					if err := flush(); err != nil {
						return nil, err
					}
					continue
				}
				seg = append(seg, plotter.XY{X: float64(d.TimesMS[j]) / 1000, Y: *v})
			}
		}
		if err := flush(); err != nil {
			return nil, err
		}
		p.Legend.Add(fmt.Sprintf("%s  %.2f%%", t.Target, t.UptimePct), &plotter.Line{LineStyle: style})
	}
	p.Legend.Add("outage", legendSwatch{0xe7, 0x4c, 0x3c, 0x88})
	p.Legend.Add("gap (sleep/restart)", legendSwatch{0x99, 0x99, 0x99, 0x88})
	if len(d.FastWindows) > 0 {
		p.Legend.Add("fast-poll window", legendSwatch{0x34, 0x98, 0xdb, 0x88})
	}
	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.TextStyle.Font.Size = vg.Points(10)

	p.X.Min, p.X.Max = xmin, xmax
	p.Y.Min, p.Y.Max = 0, ymax
	format := "3:04 PM"
	if xmax-xmin > 20*3600 {
		format = "Jan 2\n3:04 PM"
	}
	p.X.Tick.Marker = plot.TimeTicks{Format: format, Time: func(t float64) time.Time { return time.Unix(int64(t), 0).In(chartLoc) }}
	p.Y.Label.Text = "latency (ms)"
	p.Add(plotter.NewGrid())

	ups := make([]string, 0, len(d.Targets))
	for _, t := range d.Targets {
		ups = append(ups, fmt.Sprintf("%s %.2f%%", t.Target, t.UptimePct))
	}
	upLine := "no probe data in window"
	if len(ups) > 0 {
		upLine = "uptime (time-weighted): " + strings.Join(ups, " · ")
	}
	p.Title.Text = fmt.Sprintf("FComT latency, window %s  (%s → %s PT)", windowLabel(d.End.Sub(d.Start)), ptTime(time.Unix(int64(xmin), 0)), ptTime(d.End))
	p.Title.TextStyle.Font.Size = vg.Points(15)
	p.X.Label.Text = fmt.Sprintf("%s\noutages: %d · gaps: %d · fast-poll windows: %d · incidents: %d · %d points from %d rounds (SQLite) · breaks = failed probes",
		upLine, len(d.Outages), len(d.Gaps), len(d.FastWindows), len(d.Incidents), d.Points, d.RawRounds)

	wt, err := p.WriterTo(vg.Length(1200)*vg.Inch/96, vg.Length(800)*vg.Inch/96, "png")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := wt.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type legendSwatch color.NRGBA

func (s legendSwatch) Thumbnail(c *draw.Canvas) {
	c.FillPolygon(color.NRGBA(s), []vg.Point{{X: c.Min.X, Y: c.Min.Y}, {X: c.Max.X, Y: c.Min.Y}, {X: c.Max.X, Y: c.Max.Y}, {X: c.Min.X, Y: c.Max.Y}})
}

func windowLabel(w time.Duration) string {
	if w >= 24*time.Hour && w%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", w/(24*time.Hour))
	}
	return strings.TrimSuffix(strings.TrimSuffix(w.String(), "0s"), "0m")
}
