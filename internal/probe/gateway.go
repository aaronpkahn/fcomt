package probe

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DetectGateway returns the default gateway IP. On macOS it parses
// `route -n get default`; on Linux it parses `ip route show default`.
func DetectGateway(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin", "freebsd":
		cmd = exec.CommandContext(ctx, "route", "-n", "get", "default")
	case "linux":
		cmd = exec.CommandContext(ctx, "ip", "route", "show", "default")
	default:
		return "", fmt.Errorf("gateway auto-detect unsupported on %s; set monitoring.gateway.host", runtime.GOOS)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("detect gateway: %w", err)
	}
	gw := ParseGateway(string(out))
	if gw == "" {
		return "", errors.New("detect gateway: no default gateway found")
	}
	return gw, nil
}

// ParseGateway extracts the gateway IP from `route -n get default` (macOS)
// or `ip route show default` (Linux) output.
func ParseGateway(out string) string {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "gateway:") {
			gw := strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
			if net.ParseIP(gw) != nil {
				return gw
			}
		}
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "default" && f[1] == "via" && net.ParseIP(f[2]) != nil {
			return f[2]
		}
	}
	return ""
}

// GatewayProber checks the local router without ICMP: it TCP-connects to
// each port in turn, and treats either an accepted connection or an active
// refusal (RST) as "reachable" because both prove the router answered.
type GatewayProber struct {
	Timeout time.Duration
	Ports   []int // default 80, 53
	Dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

func (p GatewayProber) Probe(ctx context.Context, target Target) Result {
	ts := time.Now().UTC()
	ports := p.Ports
	if target.Port != 0 {
		ports = []int{target.Port}
	}
	if len(ports) == 0 {
		ports = []int{80, 53}
	}
	dial := p.Dial
	if dial == nil {
		// Split the budget so a silent port 80 doesn't starve the port-53 try.
		per := p.Timeout / time.Duration(len(ports))
		if per <= 0 {
			per = time.Second
		}
		d := &net.Dialer{Timeout: per}
		dial = d.DialContext
	}
	var lastErr error
	for _, port := range ports {
		start := time.Now()
		conn, err := dial(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(port)))
		if err == nil {
			_ = conn.Close()
			return Result{Target: target.Name, Kind: target.Kind, Timestamp: ts, Latency: time.Since(start), Success: true}
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return Result{Target: target.Name, Kind: target.Kind, Timestamp: ts, Latency: time.Since(start), Success: true}
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	msg := "unreachable"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	return Result{Target: target.Name, Kind: target.Kind, Timestamp: ts, Success: false, Error: msg}
}

// MultiProber routes gateway targets to Gateway and everything else to Default.
type MultiProber struct {
	Default Prober
	Gateway Prober
}

func (m MultiProber) Probe(ctx context.Context, t Target) Result {
	if t.Kind == KindGateway && m.Gateway != nil {
		return m.Gateway.Probe(ctx, t)
	}
	return m.Default.Probe(ctx, t)
}
