package consistenthash

import (
	"math/rand"
	"testing"
)

// TestBisectComparisonBound proves every start-point lookup uses at most
// ceil(log2(P))+1 key comparisons, at P=100 and P=100000, exercising every
// return position (hits, between points, and wrap-around).
func TestBisectComparisonBound(t *testing.T) {
	for _, p := range []int{1, 2, 3, 7, 100, 100000} {
		// Assign even positions 2..2p, up to 64 points per node, so odd
		// probes always land strictly between two registered points.
		r, _ := New(1, 1)
		remaining := p
		nodeID := int64(1)
		nextPoint := uint64(2)
		for remaining > 0 {
			m := remaining
			if m > maxPointsPerNode {
				m = maxPointsPerNode
			}
			pts := make([]uint64, m)
			for j := 0; j < m; j++ {
				pts[j] = nextPoint
				nextPoint += 2
			}
			if err := r.AddNode(nodeID, pts); err != nil {
				t.Fatalf("p=%d AddNode: %v", p, err)
			}
			remaining -= m
			nodeID++
		}

		if got := r.pointsCountForTest(); got != p {
			t.Fatalf("p=%d points=%d", p, got)
		}
		r.resetBisectForTest()

		// Probe at 0 (before all), every even value (hits), odd values
		// (between), and 2p+1/^uint64(0) (wrap past the largest).
		probes := []uint64{0, 1, 2, uint64(2*p - 1), uint64(2 * p), uint64(2*p + 1), ^uint64(0)}
		for _, q := range probes {
			r.locateStartForTest(q)
		}
		rng := rand.New(rand.NewSource(int64(p)))
		for i := 0; i < 5000; i++ {
			r.locateStartForTest(rng.Uint64())
		}

		total, maxComps := r.bisectStats()
		bound := bisectBoundForTest(p)
		if maxComps > bound {
			t.Fatalf("P=%d max comparisons=%d exceeds bound ceil(log2 P)+1=%d (calls=%d)",
				p, maxComps, bound, total)
		}
		if p >= 100 && maxComps < bound-1 {
			// Sanity: the bound must actually be tight-ish; a counter that
			// never increments would silently vacuously pass.
			t.Fatalf("P=%d suspiciously low maxComps=%d (bound=%d, calls=%d)",
				p, maxComps, bound, total)
		}
		t.Logf("P=%d bisect calls=%d max comparisons=%d bound=ceil(log2 P)+1=%d",
			p, total, maxComps, bound)
	}
}
