package routewatch_test

import (
	"fmt"
	"testing"

	"ontology/routewatch"
)

func bigCfg(n int) routewatch.Config {
	stops := make([]routewatch.Stop, n)
	legs := make([]leg, 0, 2*n+1)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("S%d", i)
		stops[i] = stop(id, routewatch.SoftWindow, 0, 1_000_000_000, 1)
		prev := "DEPOT"
		if i > 0 {
			prev = fmt.Sprintf("S%d", i-1)
		}
		legs = append(legs, leg{from: prev, to: id, d: 10})
	}
	return newCfg(0, stops, legs, 1_000_000_000, 0, 0, 0)
}

// BenchmarkCancelLastStop measures the cost of canceling the final stop
// while the number of preceding stops grows. If re-simulation touched the
// prefix, ns/op would grow linearly with n; a flat result demonstrates the
// suffix-only bound (the suffix has constant size 1 here).
func BenchmarkCancelLastStop(b *testing.B) {
	for _, n := range []int{64, 256, 1024, 4096} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			cfg := bigCfg(n)
			b.ReportAllocs()
			monitors := make([]*routewatch.Monitor, b.N)
			for k := range monitors {
				m, err := routewatch.New(cfg)
				if err != nil {
					b.Fatal(err)
				}
				monitors[k] = m
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				m := monitors[k]
				if _, err := m.CancelStop(fmt.Sprintf("S%d", n-1), 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCancelMid compares the cost of canceling the middle stop against
// canceling the last stop at the same route length: the ratio should track
// suffix length, never total route length.
func BenchmarkCancelMid(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			cfg := bigCfg(n)
			b.ReportAllocs()
			monitors := make([]*routewatch.Monitor, b.N)
			for k := range monitors {
				m, err := routewatch.New(cfg)
				if err != nil {
					b.Fatal(err)
				}
				monitors[k] = m
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				m := monitors[k]
				if _, err := m.CancelStop(fmt.Sprintf("S%d", n/2), 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
