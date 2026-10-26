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
