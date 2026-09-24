package ontology

import (
	"fmt"
	"sort"
)

// nodeName returns a stable, zero-padded node ID ("node-00", ...).
func nodeName(i int) string {
	return fmt.Sprintf("node-%02d", i)
}

// testKeyCount is the fixed, deterministic key corpus used by the migration
// and balance assertions.
const testKeyCount = 100000

// generateKeys produces n keys by a fixed rule, so every test run hashes an
// identical corpus and the resulting statistics are deterministic.
func generateKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("ontology/test/key#%06d", i)
	}
	return keys
}

// locateAll returns the owner of every key in order.
func locateAll(r *Ring, keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		node, err := r.Locate(k)
		if err != nil {
			panic(fmt.Sprintf("unexpected Locate error: %v", err))
		}
		out[i] = node
	}
	return out
}

// loadStats maps node ID -> number of keys it owns.
func loadStats(owners []string) map[string]int {
	m := make(map[string]int)
	for _, node := range owners {
		m[node]++
	}
	return m
}

// maxMinRatio returns max(share)/min(share) over a fixed node set. Nodes with
// zero load contribute a minimum of zero, which the caller handles.
func maxMinRatio(stats map[string]int, nodes []string) float64 {
	maxLoad := 0
	minLoad := -1
	for _, id := range nodes {
		v := stats[id]
		if v > maxLoad {
			maxLoad = v
		}
		if minLoad == -1 || v < minLoad {
			minLoad = v
		}
	}
	return float64(maxLoad) / float64(minLoad)
}

// newHandcraftedRing builds a ring whose vnodes sit exactly at the supplied
// positions, bypassing the hash function so wrap-around semantics can be
// tested with boundary keys.
func newHandcraftedRing(entries []vnode) *Ring {
	ring := New()
	ring.ring = append(ring.ring, entries...)
	sort.Slice(ring.ring, func(i, j int) bool {
		if ring.ring[i].pos != ring.ring[j].pos {
			return ring.ring[i].pos < ring.ring[j].pos
		}
		return ring.ring[i].node < ring.ring[j].node
	})
	for _, e := range entries {
		ring.nodes[e.node]++
	}
	return ring
}
