package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Profile string

const (
	ProfileLocalOnly      Profile = "local_only"
	ProfileServerAssisted Profile = "server_assisted"
)

type AppConfig struct {
	Agent      AgentConfig      `yaml:"agent" json:"agent"`
	Monitoring MonitoringConfig `yaml:"monitoring" json:"monitoring"`
	SLA        SLAConfig        `yaml:"sla" json:"sla"`
	API        APIConfig        `yaml:"api" json:"api"`
	Storage    StorageConfig    `yaml:"storage" json:"storage"`
}

// StorageConfig controls the on-disk SQLite history. An empty path means
// ~/Library/Application Support/fcomt/fcomt.db.
type StorageConfig struct {
	Path string `yaml:"path" json:"path"`
	// LoadHistoryHours is how much history is loaded into memory on startup.
	LoadHistoryHours int `yaml:"load_history_hours" json:"load_history_hours"`
}

// GatewayConfig controls probing of the local router. When Host is empty the
// default gateway is auto-detected (macOS: route -n get default).
type GatewayConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Host    string `yaml:"host" json:"host"`
	Port    int    `yaml:"port" json:"port"`
}

type AgentConfig struct {
	ID        string `yaml:"id" json:"id"`
	Mode      string `yaml:"mode" json:"mode"`
	ListenUDP int    `yaml:"listen_udp" json:"listen_udp"`
	ListenTCP int    `yaml:"listen_tcp" json:"listen_tcp"`
}

type MonitoringConfig struct {
	Profile                Profile        `yaml:"profile" json:"profile"`
	LocalProbeIntervalS    int            `yaml:"local_probe_interval_s" json:"local_probe_interval_s"`
	ServerProbeIntervalMS  int            `yaml:"server_probe_interval_ms" json:"server_probe_interval_ms"`
	ProbeTimeoutS          int            `yaml:"probe_timeout_s" json:"probe_timeout_s"`
	SessionTimeoutS        int            `yaml:"session_timeout_s" json:"session_timeout_s"`
	Destinations           []TargetConfig `yaml:"destinations" json:"destinations"`
	Peers                  []TargetConfig `yaml:"peers" json:"peers"`
	EnablePublicSpeedtests bool           `yaml:"enable_public_speedtests" json:"enable_public_speedtests"`
	Gateway                GatewayConfig  `yaml:"gateway" json:"gateway"`
	// GapFactor: a round arriving more than GapFactor x interval after the
	// previous one is recorded as a monitoring gap (e.g. machine asleep).
	GapFactor float64 `yaml:"gap_factor" json:"gap_factor"`
	// FastPoll temporarily raises the probe cadence after a failure so the
	// start and end of an outage are known to within a couple of seconds.
	FastPoll FastPollConfig `yaml:"fast_poll" json:"fast_poll"`
}

// FastPollConfig controls the fast-poll-on-failure window. The window opens on
// the first round in which any external destination fails, probes only the
// gateway plus Targets every IntervalMS (each probe limited to
// ProbeTimeoutMS), and closes TailS seconds after the first fully successful
// round. If the window has been open for MaxFastS seconds it drops to
// SlowIntervalMS until recovery (then the same tail applies).
type FastPollConfig struct {
	Enabled        bool `yaml:"enabled" json:"enabled"`
	IntervalMS     int  `yaml:"interval_ms" json:"interval_ms"`
	ProbeTimeoutMS int  `yaml:"probe_timeout_ms" json:"probe_timeout_ms"`
	TailS          int  `yaml:"tail_s" json:"tail_s"`
	MaxFastS       int  `yaml:"max_fast_s" json:"max_fast_s"`
	SlowIntervalMS int  `yaml:"slow_interval_ms" json:"slow_interval_ms"`
	// Targets are the destinations probed in fast mode (the gateway is always
	// included). Names that match no destination are ignored; if none match,
	// fast rounds probe every target.
	Targets []string `yaml:"targets" json:"targets"`
}

func (f FastPollConfig) Interval() time.Duration {
	return time.Duration(f.IntervalMS) * time.Millisecond
}
func (f FastPollConfig) SlowInterval() time.Duration {
	return time.Duration(f.SlowIntervalMS) * time.Millisecond
}
func (f FastPollConfig) ProbeTimeout() time.Duration {
	return time.Duration(f.ProbeTimeoutMS) * time.Millisecond
}
func (f FastPollConfig) Tail() time.Duration    { return time.Duration(f.TailS) * time.Second }
func (f FastPollConfig) MaxFast() time.Duration { return time.Duration(f.MaxFastS) * time.Second }

// DefaultFastPoll: 2s cadence, 1s timeout, 60s tail, 10 min cap then 10s.
func DefaultFastPoll() FastPollConfig {
	return FastPollConfig{
		Enabled:        true,
		IntervalMS:     2000,
		ProbeTimeoutMS: 1000,
		TailS:          60,
		MaxFastS:       600,
		SlowIntervalMS: 10000,
		Targets:        []string{"cloudflare-dns", "google-dns"},
	}
}

type TargetConfig struct {
	Name string `yaml:"name" json:"name"`
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
	// LatencyMSMax is a per-target average latency limit; 0 disables it.
	LatencyMSMax float64 `yaml:"latency_ms_max" json:"latency_ms_max"`
}

type SLAConfig struct {
	Profile            string  `yaml:"profile" json:"profile"`
	LatencyMSMax       float64 `yaml:"latency_ms_max" json:"latency_ms_max"`
	JitterMSMax        float64 `yaml:"jitter_ms_max" json:"jitter_ms_max"`
	PacketLossPctMax   float64 `yaml:"packet_loss_pct_max" json:"packet_loss_pct_max"`
	AvailabilityPctMin float64 `yaml:"availability_pct_min" json:"availability_pct_min"`
	// ConsecutiveRounds is how many consecutive bad rounds are needed before a
	// target incident opens (avoids single-probe noise). Minimum 1.
	ConsecutiveRounds int `yaml:"consecutive_rounds" json:"consecutive_rounds"`
}

type APIConfig struct {
	Listen string `yaml:"listen" json:"listen"`
	// EnableDemo allows POST /v1/examples/seed to load fake data. Off by default.
	EnableDemo bool `yaml:"enable_demo" json:"enable_demo"`
}

func Default() AppConfig {
	return AppConfig{
		Agent: AgentConfig{
			ID:        "site-local-01",
			Mode:      "client_only",
			ListenUDP: 23999,
			ListenTCP: 23999,
		},
		Monitoring: MonitoringConfig{
			Profile:               ProfileLocalOnly,
			LocalProbeIntervalS:   30,
			ServerProbeIntervalMS: 500,
			ProbeTimeoutS:         3,
			SessionTimeoutS:       30,
			Destinations: []TargetConfig{
				{Name: "google-dns", Host: "8.8.8.8", Port: 53},
				{Name: "cloudflare-dns", Host: "1.1.1.1", Port: 53},
				{Name: "aws-us-east-1", Host: "ec2.us-east-1.amazonaws.com", Port: 443},
			},
			Peers:                  nil,
			EnablePublicSpeedtests: true,
			Gateway:                GatewayConfig{Enabled: true},
			GapFactor:              3,
			FastPoll:               DefaultFastPoll(),
		},
		SLA: SLAConfig{
			Profile:            "isp-primary",
			LatencyMSMax:       0, // global latency check off; use per-target latency_ms_max
			JitterMSMax:        0,
			ConsecutiveRounds:  2,
			PacketLossPctMax:   0.5,
			AvailabilityPctMin: 99.9,
		},
		API: APIConfig{
			Listen: "127.0.0.1:8080",
		},
		Storage: StorageConfig{LoadHistoryHours: 7 * 24},
	}
}

func Load(path string) (AppConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

func (c AppConfig) Validate() error {
	if c.Monitoring.Profile != ProfileLocalOnly && c.Monitoring.Profile != ProfileServerAssisted {
		return fmt.Errorf("invalid monitoring.profile: %q", c.Monitoring.Profile)
	}
	if c.Monitoring.LocalProbeIntervalS < 30 {
		return errors.New("monitoring.local_probe_interval_s must be >= 30")
	}
	if c.Monitoring.ServerProbeIntervalMS <= 0 {
		return errors.New("monitoring.server_probe_interval_ms must be > 0")
	}
	if c.Monitoring.ProbeTimeoutS <= 0 {
		return errors.New("monitoring.probe_timeout_s must be > 0")
	}
	if len(c.Monitoring.Destinations) == 0 {
		return errors.New("monitoring.destinations must include at least one target")
	}
	for _, t := range c.Monitoring.Destinations {
		if err := validateTarget(t); err != nil {
			return fmt.Errorf("destination %q: %w", t.Name, err)
		}
	}
	for _, t := range c.Monitoring.Peers {
		if err := validateTarget(t); err != nil {
			return fmt.Errorf("peer %q: %w", t.Name, err)
		}
	}
	if c.Monitoring.Gateway.Port < 0 || c.Monitoring.Gateway.Port > 65535 {
		return fmt.Errorf("monitoring.gateway.port out of range: %d", c.Monitoring.Gateway.Port)
	}
	if c.Monitoring.GapFactor != 0 && c.Monitoring.GapFactor < 1.5 {
		return errors.New("monitoring.gap_factor must be >= 1.5 (or 0 for default 3)")
	}
	if fp := c.Monitoring.FastPoll; fp.Enabled {
		switch {
		case fp.IntervalMS <= 0:
			return errors.New("monitoring.fast_poll.interval_ms must be > 0")
		case fp.ProbeTimeoutMS <= 0 || fp.ProbeTimeoutMS > fp.IntervalMS:
			return errors.New("monitoring.fast_poll.probe_timeout_ms must be > 0 and <= interval_ms")
		case fp.TailS < 0:
			return errors.New("monitoring.fast_poll.tail_s must be >= 0")
		case fp.MaxFastS <= 0:
			return errors.New("monitoring.fast_poll.max_fast_s must be > 0")
		case fp.SlowIntervalMS < fp.IntervalMS:
			return errors.New("monitoring.fast_poll.slow_interval_ms must be >= interval_ms")
		}
	}
	if c.SLA.ConsecutiveRounds < 0 {
		return errors.New("sla.consecutive_rounds must be >= 0")
	}
	if c.API.Listen == "" {
		return errors.New("api.listen cannot be empty")
	}
	return nil
}

func (m MonitoringConfig) ProbeInterval() time.Duration {
	if m.Profile == ProfileServerAssisted {
		return time.Duration(m.ServerProbeIntervalMS) * time.Millisecond
	}
	return time.Duration(m.LocalProbeIntervalS) * time.Second
}

func (m MonitoringConfig) ProbeTimeout() time.Duration {
	return time.Duration(m.ProbeTimeoutS) * time.Second
}

func validateTarget(t TargetConfig) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("name cannot be empty")
	}
	if strings.TrimSpace(t.Host) == "" {
		return errors.New("host cannot be empty")
	}
	if t.LatencyMSMax < 0 {
		return errors.New("latency_ms_max must be >= 0")
	}
	if t.Port < 0 || t.Port > 65535 {
		return fmt.Errorf("port out of range: %d", t.Port)
	}
	return nil
}

// GapThreshold returns the wall-clock delay between rounds that counts as a gap.
func (m MonitoringConfig) GapThreshold() time.Duration {
	f := m.GapFactor
	if f == 0 {
		f = 3
	}
	return time.Duration(f * float64(m.ProbeInterval()))
}
