package examples

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aaron/fcomt-agent/internal/incident"
	"github.com/aaron/fcomt-agent/internal/metrics"
	"github.com/aaron/fcomt-agent/internal/probe"
)

//go:embed testdata/*.json
var embedded embed.FS

type Sample struct {
	Target     string  `json:"target"`
	Kind       string  `json:"kind"`
	SecondsAgo int     `json:"seconds_ago"`
	Success    bool    `json:"success"`
	LatencyMS  float64 `json:"latency_ms"`
	Error      string  `json:"error,omitempty"`
}

type Event struct {
	Target     string `json:"target"`
	SecondsAgo int    `json:"seconds_ago"`
	Severity   string `json:"severity"`
	State      string `json:"state"`
	Message    string `json:"message"`
}

type Scenario struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Profile     string   `json:"profile"`
	Samples     []Sample `json:"samples"`
	Events      []Event  `json:"events"`
}

type ScenarioMeta struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Profile     string `json:"profile"`
	SampleCount int    `json:"sample_count"`
}

type Library struct {
	byName map[string]Scenario
	err    error
}

func NewLibrary() *Library {
	files, err := embedded.ReadDir("testdata")
	if err != nil {
		return &Library{byName: map[string]Scenario{}, err: err}
	}

	out := make(map[string]Scenario, len(files))
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, readErr := embedded.ReadFile(filepath.Join("testdata", f.Name()))
		if readErr != nil {
			return &Library{byName: out, err: readErr}
		}

		var s Scenario
		if unmarshalErr := json.Unmarshal(raw, &s); unmarshalErr != nil {
			return &Library{byName: out, err: fmt.Errorf("%s: %w", f.Name(), unmarshalErr)}
		}
		if strings.TrimSpace(s.Name) == "" {
			s.Name = strings.TrimSuffix(f.Name(), ".json")
		}
		out[s.Name] = s
	}

	return &Library{byName: out}
}

func (l *Library) LoadError() error {
	if l == nil {
		return nil
	}
	return l.err
}

func (l *Library) List() []ScenarioMeta {
	if l == nil {
		return nil
	}

	metas := make([]ScenarioMeta, 0, len(l.byName))
	for _, s := range l.byName {
		metas = append(metas, ScenarioMeta{
			Name:        s.Name,
			Title:       s.Title,
			Description: s.Description,
			Profile:     s.Profile,
			SampleCount: len(s.Samples),
		})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
	return metas
}

func (l *Library) Seed(name string, collector *metrics.Collector, store *incident.Store, now time.Time) (ScenarioMeta, error) {
	if l == nil {
		return ScenarioMeta{}, fmt.Errorf("scenario library unavailable")
	}
	if l.err != nil {
		return ScenarioMeta{}, l.err
	}
	s, ok := l.byName[name]
	if !ok {
		return ScenarioMeta{}, fmt.Errorf("scenario %q not found", name)
	}

	collector.Reset()
	store.Reset()

	for _, sample := range s.Samples {
		kind := probe.KindDestination
		if strings.EqualFold(sample.Kind, string(probe.KindPeer)) {
			kind = probe.KindPeer
		}
		latency := time.Duration(sample.LatencyMS * float64(time.Millisecond))
		collector.Add(probe.Result{
			Target:    sample.Target,
			Kind:      kind,
			Timestamp: now.Add(-time.Duration(sample.SecondsAgo) * time.Second).UTC(),
			Latency:   latency,
			Success:   sample.Success,
			Error:     sample.Error,
		})
	}

	for _, e := range s.Events {
		store.Add(incident.Event{
			Time:     now.Add(-time.Duration(e.SecondsAgo) * time.Second).UTC(),
			Target:   e.Target,
			Severity: e.Severity,
			State:    e.State,
			Message:  e.Message,
		})
	}

	return ScenarioMeta{
		Name:        s.Name,
		Title:       s.Title,
		Description: s.Description,
		Profile:     s.Profile,
		SampleCount: len(s.Samples),
	}, nil
}
