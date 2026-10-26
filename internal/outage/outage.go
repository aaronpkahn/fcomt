// Package outage turns aligned probe rounds into internet-down periods.
package outage

import (
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

const (
	CauseISP   = "isp"   // gateway answered, everything beyond it failed
	CauseLocal = "local" // gateway also unreachable (Wi-Fi/router/Mac problem)
)

// Transition describes a change in outage state produced by a round.
type Transition struct {
	Opened bool
	Closed bool
	Start  time.Time
	End    time.Time
	Cause  string
}

// Detector opens an outage when every external destination fails in the same
// round and closes it when any external destination succeeds.
type Detector struct {
	open  bool
	start time.Time
	cause string
}

// Restore marks an outage as already open (e.g. resumed from the DB).
func (d *Detector) Restore(start time.Time, cause string) {
	d.open, d.start, d.cause = true, start, cause
}

func (d *Detector) Open() bool { return d.open }

// Observe processes one round of results taken at roundTime.
func (d *Detector) Observe(roundTime time.Time, results []probe.Result) Transition {
	external, extOK := 0, 0
	gatewaySeen, gatewayOK := false, false
	for _, r := range results {
		switch {
		case r.Kind == probe.KindGateway:
			gatewaySeen = true
			gatewayOK = gatewayOK || r.Success
		case r.Kind.IsExternal():
			external++
			if r.Success {
				extOK++
			}
		}
	}
	if external == 0 {
		return Transition{}
	}
	allFailed := extOK == 0

	if allFailed && !d.open {
		cause := CauseISP
		if gatewaySeen && !gatewayOK {
			cause = CauseLocal
		}
		d.open, d.start, d.cause = true, roundTime, cause
		return Transition{Opened: true, Start: roundTime, Cause: cause}
	}
	if !allFailed && d.open {
		t := Transition{Closed: true, Start: d.start, End: roundTime, Cause: d.cause}
		d.open = false
		return t
	}
	return Transition{}
}
