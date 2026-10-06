package cg

import (
	"fmt"
	"testing"
)

// These benchmarks are the executable evidence for the two constant-time
// claims:
//
//  1. deciding whether a write must queue does not grow with group size
//     or already-queued write count (BenchmarkWriteQueueDecision);
//  2. a non-final ConfirmFreeze decides "am I the last?" in O(1) without
//     scanning members (BenchmarkConfirmNonLast).
//
// Run: go test -bench=. -benchmem ./cg

func benchGroup(b *testing.B, n int) (*Coordinator, string) {
	b.Helper()
	vols := make([]string, n)
	for i := range vols {
		vols[i] = fmt.Sprintf("v%d", i)
	}
	c := New(Config{DefaultQueueCapacity: 1_000_000_000, MaxFreezeHold: 1_000_000})
	if err := c.CreateGroup(0, GroupSpec{GroupID: "g", VolumeIDs: vols}); err != nil {
		b.Fatal(err)
	}
	if _, err := c.BeginSnapshot(1, "g", 1_000_000); err != nil {
		b.Fatal(err)
	}
	return c, vols[n-1]
}

func BenchmarkWriteQueueDecision(b *testing.B) {
	for _, n := range []int{2, 4, 8, 16} {
		b.Run(fmt.Sprintf("members=%d", n), func(b *testing.B) {
			c, target := benchGroup(b, n)
			// Confirm every volume except the target so writes to target
			// must queue during freezing; prefill a large queue on it.
			for i := 0; i < n-1; i++ {
				if _, err := c.ConfirmFreeze(1, "g", fmt.Sprintf("v%d", i)); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < 1000; i++ {
				if _, err := c.Write(1, target, "prefill"); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// This write keeps queuing; the decision itself is O(1)
				// even though the queue holds 1000 items.
				if _, err := c.Write(1, target, "x"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkConfirmNonLast(b *testing.B) {
	for _, n := range []int{2, 4, 8, 16} {
		b.Run(fmt.Sprintf("members=%d", n), func(b *testing.B) {
			// Rebuild per iteration (untimed), then time only the
			// confirmation decision. The timed confirmation is always
			// non-final: two volumes remain pending beforehand, so its
			// cost must not depend on group size.
			for k := 0; k < b.N; k++ {
				c, _ := benchGroup(b, n)
				for i := 2; i < n; i++ {
					if _, err := c.ConfirmFreeze(1, "g", fmt.Sprintf("v%d", i)); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if _, err := c.ConfirmFreeze(1, "g", "v0"); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
			}
		})
	}
}
