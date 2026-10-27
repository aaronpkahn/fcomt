package probe

import (
	"context"
	"net"
	"strconv"
	"time"
)

type Kind string

const (
	KindDestination Kind = "destination"
	KindPeer        Kind = "peer"
	// KindGateway is the local router; it is excluded from the
	// "all external destinations failed" outage test.
	KindGateway Kind = "gateway"
)

// IsExternal reports whether results of this kind count toward internet reachability.
func (k Kind) IsExternal() bool { return k == KindDestination }

type Target struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Kind Kind   `json:"kind"`
}

type Result struct {
	Target    string        `json:"target"`
	Kind      Kind          `json:"kind"`
	Timestamp time.Time     `json:"timestamp"`
	Latency   time.Duration `json:"latency"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	// Mode is the polling mode the round ran in: "normal" (regular cadence),
	// "fast" or "slow" (fast-poll window after a failure). Empty means normal.
	Mode string `json:"mode,omitempty"`
}

const (
	ModeNormal = "normal"
	ModeFast   = "fast"
	ModeSlow   = "slow"
)

// NormalizedMode returns r.Mode, treating empty as ModeNormal.
func (r Result) NormalizedMode() string {
	if r.Mode == "" {
		return ModeNormal
	}
	return r.Mode
}

type Prober interface {
	Probe(ctx context.Context, target Target) Result
}

type TCPProber struct {
	Timeout time.Duration
	Dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

func (p TCPProber) Probe(ctx context.Context, target Target) Result {
	ts := time.Now().UTC()
	port := target.Port
	if port == 0 {
		port = 443
	}
	addr := net.JoinHostPort(target.Host, strconv.Itoa(port))

	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: p.Timeout}
		dial = d.DialContext
	}

	start := time.Now()
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return Result{
			Target:    target.Name,
			Kind:      target.Kind,
			Timestamp: ts,
			Success:   false,
			Error:     err.Error(),
		}
	}
	_ = conn.Close()

	return Result{
		Target:    target.Name,
		Kind:      target.Kind,
		Timestamp: ts,
		Latency:   time.Since(start),
		Success:   true,
	}
}
