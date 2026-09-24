package ontology

import "testing"

// With 10 nodes x 200 vnodes and 100k fixed keys, every node must carry
// between 0.5/10 and 2.0/10 of the keys. The key set and hash are fixed,
// so this is a deterministic assertion, not a statistical hope.
func TestBalanceWithManyVnodes(t *testing.T) {
	ids := nodeIDs(10)
	counts := distribution(t, ids, 200, 100000)
	for _, id := range ids {
		share := float64(counts[id]) / 100000.0
		if lo, hi := 0.5/10.0, 2.0/10.0; share < lo || share > hi {
			t.Fatalf("node %q carries share %v (%d keys), outside [%v, %v]",
				id, share, counts[id], lo, hi)
		}
	}
}

// Virtual nodes must actually help: with vnodes=1 the max/min load ratio
// must be strictly worse (larger) than with vnodes=200.
func TestSingleVnodeIsLessBalanced(t *testing.T) {
	ids := nodeIDs(10)
	ratio1 := maxMinRatio(distribution(t, ids, 1, 100000), ids)
	ratio200 := maxMinRatio(distribution(t, ids, 200, 100000), ids)
	t.Logf("max/min ratio: vnodes=1 -> %.2f, vnodes=200 -> %.2f", ratio1, ratio200)
	if ratio1 <= ratio200 {
		t.Fatalf("vnodes=1 max/min ratio %.2f not worse than vnodes=200 ratio %.2f",
			ratio1, ratio200)
	}
}
