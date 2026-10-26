package export

import (
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/storage"
)

type EvidenceBundle struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Profile     config.Profile    `json:"profile"`
	Window      string            `json:"window"`
	Start       time.Time         `json:"start"`
	End         time.Time         `json:"end"`
	Summaries   []metrics.Summary `json:"summaries"`
	Incidents   []incident.Event  `json:"incidents"`
	Outages     []storage.Outage  `json:"outages"`
	Gaps        []storage.Gap     `json:"gaps"`
}

// BuildEvidence bundles everything inside [now-window, now]. db may be nil,
// in which case incidents come from the in-memory store and outages/gaps are empty.
func BuildEvidence(
	cfg config.AppConfig,
	collector *metrics.Collector,
	store *incident.Store,
	db *storage.DB,
	window time.Duration,
	now time.Time,
) (EvidenceBundle, error) {
	now = now.UTC()
	start := now.Add(-window)
	b := EvidenceBundle{
		GeneratedAt: now,
		Profile:     cfg.Monitoring.Profile,
		Window:      window.String(),
		Start:       start,
		End:         now,
		Summaries:   collector.Summaries(window, now),
		Incidents:   []incident.Event{},
		Outages:     []storage.Outage{},
		Gaps:        []storage.Gap{},
	}
	if db == nil {
		b.Incidents = store.Between(start, now)
		return b, nil
	}
	var err error
	if b.Incidents, err = db.Incidents(start, now, 0); err != nil {
		return b, err
	}
	if b.Outages, err = db.Outages(start, now); err != nil {
		return b, err
	}
	if b.Gaps, err = db.Gaps(start, now); err != nil {
		return b, err
	}
	if b.Incidents == nil {
		b.Incidents = []incident.Event{}
	}
	if b.Outages == nil {
		b.Outages = []storage.Outage{}
	}
	if b.Gaps == nil {
		b.Gaps = []storage.Gap{}
	}
	return b, nil
}
