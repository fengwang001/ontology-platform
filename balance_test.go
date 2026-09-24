package ontology

import "testing"

func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = nodeName(i)
	}
	return ids
}

// With 200 vnodes per node, each of 10 nodes must own a share in
// [0.5/10, 2.0/10] of the fixed 100k-key corpus.
func TestBalanceWithVnodes200(t *testing.T) {
	const nodes, vnodes = 10, 200

	ring := buildRing(t, nodeIDs(nodes), vnodes)
	stats := loadStats(locateAll(ring, generateKeys(testKeyCount)))

	lo := 0.5 / nodes
	hi := 2.0 / nodes
	for _, id := range nodeIDs(nodes) {
		share := float64(stats[id]) / float64(testKeyCount)
		if share < lo || share > hi {
			t.Fatalf("node %q share %.5f outside [%v, %v]", id, share, lo, hi)
		}
	}
}

// A single vnode per node is allowed to be uneven; its max/min ratio must be
// worse than the vnode-rich ring's, demonstrating vnodes actually balance.
func TestVnodes1MoreUnevenThanVnodes200(t *testing.T) {
	const nodes = 10
	keys := generateKeys(testKeyCount)

	one := buildRing(t, nodeIDs(nodes), 1)
	many := buildRing(t, nodeIDs(nodes), 200)

	statsOne := loadStats(locateAll(one, keys))
	statsMany := loadStats(locateAll(many, keys))

	ratioOne := maxMinRatio(statsOne, nodeIDs(nodes))
	ratioMany := maxMinRatio(statsMany, nodeIDs(nodes))

	if ratioOne <= ratioMany {
		t.Fatalf("vnodes=1 ratio %.4f not greater than vnodes=200 ratio %.4f",
			ratioOne, ratioMany)
	}
	if ratioMany >= 1.25 {
		t.Fatalf("vnodes=200 unexpectedly uneven: ratio %.4f", ratioMany)
	}
}
