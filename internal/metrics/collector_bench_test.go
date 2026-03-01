package metrics

import (
	"testing"
	"time"

	"github.com/aaron/fcomt-agent/internal/probe"
)

func BenchmarkCollectorAdd(b *testing.B) {
	c := NewCollector(24 * time.Hour)
	now := time.Now().UTC()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c.Add(probe.Result{
			Target:    "google",
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Success:   i%10 != 0,
			Latency:   time.Duration(5+i%20) * time.Millisecond,
		})
	}
}
