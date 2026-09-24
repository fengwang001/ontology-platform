package ring

import "testing"

// TestAddNodeMoveRatio adds an 11th node to a 10-node ring and asserts,
// per key, that the moved fraction lies in [1/22, 3/22] (theory: 1/11).
func TestAddNodeMoveRatio(t *testing.T) {
	keys := genKeys(testKeyCount)
	r := buildRing(t, testNodeCount, testVnodes)
	before := locateAll(t, r, keys)

	if err := r.Add(nodeID(testNodeCount), testVnodes); err != nil {
		t.Fatalf("Add 11th node: %v", err)
	}
	after := locateAll(t, r, keys)

	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	ratio := float64(moved) / float64(len(keys))
	const lo = 1.0 / 22.0
	const hi = 3.0 / 22.0
	t.Logf("moved %d of %d keys, ratio=%.4f, theory=1/11=%.4f",
		moved, len(keys), ratio, 1.0/11.0)
	if ratio < lo || ratio > hi {
		t.Fatalf("move ratio %.4f outside [%.4f, %.4f]", ratio, lo, hi)
	}
}

// TestRemoveNodeOnlyMovesOwnedKeys verifies the defining property of
// consistent hashing: removing a node reassigns only the keys that node
// owned; every other key keeps its exact owner, checked key by key.
func TestRemoveNodeOnlyMovesOwnedKeys(t *testing.T) {
	keys := genKeys(testKeyCount)
	r := buildRing(t, testNodeCount, testVnodes)
	before := locateAll(t, r, keys)

	victim := nodeID(3)
	if err := r.Remove(victim); err != nil {
		t.Fatalf("Remove(%s): %v", victim, err)
	}
	after := locateAll(t, r, keys)

	movedFromVictim := 0
	for i := range keys {
		if before[i] == victim {
			if after[i] == victim {
				t.Fatalf("key %q still maps to removed node %s", keys[i], victim)
			}
			movedFromVictim++
			continue
		}
		if after[i] != before[i] {
			t.Fatalf("key %q moved from %s to %s though %s was removed",
				keys[i], before[i], after[i], victim)
		}
	}
	if movedFromVictim == 0 {
		t.Fatal("removed node owned no keys; test is vacuous")
	}
	t.Logf("%d keys owned by %s were reassigned, all others untouched",
		movedFromVictim, victim)
}
