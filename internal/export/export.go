package export

import (
	"time"

	"github.com/aaron/fcomt-agent/internal/config"
	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
)

type EvidenceBundle struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Profile     config.Profile    `json:"profile"`
	Window      string            `json:"window"`
	Summaries   []metrics.Summary `json:"summaries"`
	Incidents   []incident.Event  `json:"incidents"`
}

func BuildEvidence(
	cfg config.AppConfig,
	collector *metrics.Collector,
	store *incident.Store,
	window time.Duration,
	now time.Time,
) EvidenceBundle {
	return EvidenceBundle{
		GeneratedAt: now.UTC(),
		Profile:     cfg.Monitoring.Profile,
		Window:      window.String(),
		Summaries:   collector.Summaries(window, now.UTC()),
		Incidents:   store.List(),
	}
}
