package routewatch

import (
	"fmt"
	"testing"
)

func benchCfg(n int) Config {
	stops := make([]Stop, n)
	dur := map[[2]string]int64{}
	for i := range stops {
		id := fmt.Sprintf("S%d", i)
		stops[i] = Stop{ID: id, Kind: SoftWindow, Window: Window{0, 1_000_000_000}, Service: 1}
		prev := "DEPOT"
		if i > 0 {
			prev = fmt.Sprintf("S%d", i-1)
		}
		dur[[2]string{prev, id}] = 10
	}
	return Config{
		RouteID: "R", DepotID: "DEPOT", Departure: 0,
		Stops:      stops,
		Travel:     TravelTable{DepotID: "DEPOT", Duration: dur},
		MaxDriving: 1_000_000_000,
	}
}

// BenchmarkResimulateLastSuffix isolates the incremental engine: after a full
// initial projection, re-simulating only the final stop (suffix size 1) must
// cost the same regardless of how many stops precede it. A flat ns/op across
// n is the machine-checkable proof of the "never scales with the prefix"
// requirement.
func BenchmarkResimulateLastSuffix(b *testing.B) {
	for _, n := range []int{256, 1024, 4096, 16384} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			cfg := benchCfg(n)
			eng := newEngine(&cfg)
			results := make([]StopResult, n)
			eng.resimulate(results, 0, nil)
			anchors := map[int]int64{n - 1: 9_999_999}
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				eng.resimulate(results, n-1, anchors)
			}
		})
	}
}

// BenchmarkResimulateHalfSuffix provides the scaling reference point: a
// suffix of n/2 should cost ~half of a full re-simulation at each n.
func BenchmarkResimulateHalfSuffix(b *testing.B) {
	for _, n := range []int{256, 1024, 4096, 16384} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			cfg := benchCfg(n)
			eng := newEngine(&cfg)
			results := make([]StopResult, n)
			eng.resimulate(results, 0, nil)
			anchors := map[int]int64{n / 2: 9_999_999}
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				eng.resimulate(results, n/2, anchors)
			}
		})
	}
}
