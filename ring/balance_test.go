package ring

import "testing"

// With 10 nodes x 200 vnodes and 100k fixed keys, every node's share
// must land in [0.5/10, 2.0/10]. Key generation is fixed, so this is a
// deterministic assertion, not a probabilistic one.
func TestLoadBalanceWithVnodes(t *testing.T) {
	keys := genKeys(testKeyCount)
	r := buildRing(t, testNodeCount, testVnodes)
	counts := distribution(t, r, keys)

	if len(counts) != testNodeCount {
		t.Fatalf("%d nodes hold keys, want %d", len(counts), testNodeCount)
	}
	const lo = 0.5 / testNodeCount
	const hi = 2.0 / testNodeCount
	for i := 0; i < testNodeCount; i++ {
		share := float64(counts[nodeID(i)]) / float64(len(keys))
		if share < lo || share > hi {
			t.Fatalf("node %s share %.4f outside [%.4f, %.4f]",
				nodeID(i), share, lo, hi)
		}
	}
	t.Logf("max/min ratio with %d vnodes: %.2f",
		testVnodes, maxMinRatio(counts))
}

// With vnodes=1 the balance must be measurably worse than with 200,
// proving virtual nodes actually do something.
func TestVnodesImproveBalance(t *testing.T) {
	keys := genKeys(testKeyCount)

	balanced := buildRing(t, testNodeCount, testVnodes)
	ratioBalanced := maxMinRatio(distribution(t, balanced, keys))

	skewed := buildRing(t, testNodeCount, testVNodeRatio)
	ratioSkewed := maxMinRatio(distribution(t, skewed, keys))

	t.Logf("max/min ratio: vnodes=%d -> %.2f, vnodes=%d -> %.2f",
		testVNodeRatio, ratioSkewed, testVnodes, ratioBalanced)
	if ratioSkewed <= ratioBalanced {
		t.Fatalf("vnodes=1 ratio %.2f not worse than vnodes=%d ratio %.2f",
			ratioSkewed, testVnodes, ratioBalanced)
	}
}
