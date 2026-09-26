package mpc

import "testing"

// TestProbeCountConstant proves the "is right v matched?" check is O(1): m
// left nodes are pre-matched to distinct right nodes, then one augmentation
// that needs to find the single free right node is performed for several m.
// The number of right nodes inspected must stay <= 1 instead of growing with m.
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		adj := make([][]int, m+1)
		for i := 0; i < m; i++ {
			adj[i] = []int{i + 1} // left i pre-matched to right i+1
		}
		adj[m] = []int{0} // new left m can only reach the one free right 0
		s := newMatcher(adj)
		for i := 0; i < m; i++ {
			s.matchL[i] = i + 1
			s.matchR[i+1] = i
		}
		if !s.tryAugment(m) {
			t.Fatalf("m=%d: augmentation failed", m)
		}
		if s.probes > 1 {
			t.Fatalf("m=%d: inspected %d right nodes, want <= 1", m, s.probes)
		}
	}
}
