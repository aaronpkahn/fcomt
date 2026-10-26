package probe

import (
	"context"
	"net"
	"syscall"
	"testing"
)

func TestParseGateway(t *testing.T) {
	mac := `   route to: default
destination: default
       mask: default
    gateway: 192.168.86.1
  interface: en0
`
	if got := ParseGateway(mac); got != "192.168.86.1" {
		t.Fatalf("mac: got %q", got)
	}
	if got := ParseGateway("default via 10.0.0.1 dev eth0 proto dhcp\n"); got != "10.0.0.1" {
		t.Fatalf("linux: got %q", got)
	}
	if got := ParseGateway("nothing here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestGatewayProberRefusedCountsAsReachable(t *testing.T) {
	p := GatewayProber{Dial: func(ctx context.Context, n, a string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}}
	if r := p.Probe(context.Background(), Target{Name: "gw", Host: "10.0.0.1", Kind: KindGateway}); !r.Success {
		t.Fatalf("expected refused to count as reachable: %+v", r)
	}
	p.Dial = func(ctx context.Context, n, a string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}
	}
	if r := p.Probe(context.Background(), Target{Name: "gw", Host: "10.0.0.1", Kind: KindGateway}); r.Success {
		t.Fatalf("expected unreachable: %+v", r)
	}
}
