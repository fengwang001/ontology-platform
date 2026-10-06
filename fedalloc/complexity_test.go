package fedalloc

import (
	"testing"
	"time"
)

// TestComplexityIndependentOfTotal verifies, reproducibly rather than by
// eyeballing timings, that allocation cost does not grow with the requested
// replica count:
//
//  1. Structural proof: there is no per-replica loop. Every round pins at
//     least one positive-weight participant (a cluster whose floor reaches its
//     cap leaves the active set), so with n participants there are at most n
//     rounds; each round costs O(n log n) for the tie-break sort. The cost is
//     therefore polynomial in the number of clusters and completely
//     independent of the replica total.
//  2. Empirical guard: one fixed cluster set is allocated at totals spanning
//     100 .. 10^15; a per-replica O(total) implementation could never keep the
//     largest case within a tight multiple of the smallest.
func TestComplexityIndependentOfTotal(t *testing.T) {
	l := newLogger(t)
	defer l.flush()

	mk := func() *Registry {
		r := NewRegistry()
		l.upsert(r, Cluster{Name: "a", Weight: 3, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "b", Weight: 7, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "c", Weight: 11, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "d", Weight: 13, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "e", Weight: 1, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "z", Weight: 0, MinReplicas: 1, Capacity: 1 << 62, Available: true})
		l.upsert(r, Cluster{Name: "down", Weight: 5, Capacity: 1 << 62, Available: false, CurrentReplicas: 1000})
		return r
	}

	totals := []int64{
		100,
		100_000,
		100_000_000,
		100_000_000_000,
		1_000_000_000_000_000, // 10^15
	}
	var durations []time.Duration
	for _, total := range totals {
		r := mk()
		if _, err := r.Allocate(total); err != nil {
			t.Fatal(err)
		}
		var best time.Duration
		for k := 0; k < 5; k++ {
			start := time.Now()
			res, err := r.Allocate(total)
			d := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			sum := int64(0)
			for _, tg := range res.Targets {
				sum += tg.Replicas
			}
			if sum != total {
				t.Fatalf("sum %d != total %d", sum, total)
			}
			if best == 0 || d < best {
				best = d
			}
		}
		durations = append(durations, best)
		l.log("total=%15d best-of-5 duration=%v (n=7 clusters fixed)", total, best)
	}

	ratio := float64(durations[len(durations)-1]) / float64(durations[0])
	if durations[0] == 0 {
		ratio = 0
	}
	// A per-replica algorithm would need a ~1e13x ratio here.
	l.check(ratio < 250, "10^15-vs-100 duration ratio %.2f stays below 250", ratio)
}
