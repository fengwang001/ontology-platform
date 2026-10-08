package endpointshard

import (
	"fmt"
	"testing"
)

// BenchmarkSyncSmallDelta measures one-endpoint-change syncs over
// services with the same endpoint count but 100x different shard
// counts; the curve must stay flat, proving the cost does not grow
// with the number of unaffected shards. (The residual cost is the
// unavoidable O(|desired|) input processing, identical in all runs.)
func BenchmarkSyncSmallDelta(b *testing.B) {
	const n = 20000
	for _, capacity := range []int{2, 20, 200} {
		b.Run(fmt.Sprintf("shards=%d", n/capacity), func(b *testing.B) {
			m := NewManager()
			m.CreateService("s", capacity)
			desired := make([]Endpoint, 0, n)
			for i := 0; i < n; i++ {
				desired = append(desired, live(fmt.Sprintf("e%06d", i), "r1"))
			}
			if _, err := m.Sync("s", desired); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				desired[0].Healthy = !desired[0].Healthy
				if _, err := m.Sync("s", desired); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkQuery measures consumer reads with a bounded result over a
// large service.
func BenchmarkQuery(b *testing.B) {
	m := NewManager()
	m.CreateService("s", 4)
	desired := make([]Endpoint, 0, 100000)
	for i := 0; i < 100000; i++ {
		e := ep(fmt.Sprintf("e%06d", i), "r1", false, false)
		if i < 16 {
			e = live(fmt.Sprintf("e%06d", i), "r1")
		}
		desired = append(desired, e)
	}
	if _, err := m.Sync("s", desired); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Query("s", "r1"); err != nil {
			b.Fatal(err)
		}
	}
}
