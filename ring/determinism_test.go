package ring

import (
	"math/rand"
	"testing"
)

// The ring layout and every key's ownership must be identical no matter
// in which order the same nodes were added. Checked over 20 permutations.
func TestAddOrderDoesNotMatter(t *testing.T) {
	const permutations = 20
	keys := genKeys(20000)

	reference := buildRing(t, testNodeCount, 50)
	refPoints := reference.Points()
	refOwners := locateAll(t, reference, keys)

	rng := rand.New(rand.NewSource(42))
	for p := 0; p < permutations; p++ {
		order := rng.Perm(testNodeCount)
		r := New()
		for _, i := range order {
			if err := r.Add(nodeID(i), 50); err != nil {
				t.Fatalf("perm %d: Add(%s): %v", p, nodeID(i), err)
			}
		}
		gotPoints := r.Points()
		if len(gotPoints) != len(refPoints) {
			t.Fatalf("perm %d: %d points, want %d", p, len(gotPoints), len(refPoints))
		}
		for i := range gotPoints {
			if gotPoints[i] != refPoints[i] {
				t.Fatalf("perm %d: point %d differs: %+v vs %+v",
					p, i, gotPoints[i], refPoints[i])
			}
		}
		gotOwners := locateAll(t, r, keys)
		for i := range keys {
			if gotOwners[i] != refOwners[i] {
				t.Fatalf("perm %d: key %q owned by %s, want %s",
					p, keys[i], gotOwners[i], refOwners[i])
			}
		}
	}
}

// Rebuilding the same ring twice in one process must give identical
// ownership for every key: the hash must not depend on a random seed.
func TestRebuildInProcessIsDeterministic(t *testing.T) {
	keys := genKeys(20000)
	first := locateAll(t, buildRing(t, testNodeCount, 50), keys)
	second := locateAll(t, buildRing(t, testNodeCount, 50), keys)
	for i := range keys {
		if first[i] != second[i] {
			t.Fatalf("key %q: %s vs %s across rebuilds", keys[i], first[i], second[i])
		}
	}
}
