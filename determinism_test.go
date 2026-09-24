package ontology

import (
	"math/rand"
	"testing"
)

// The ring must be a pure function of the node set: 20 different add
// orders (deterministic shuffles of the same 10 IDs) must produce the
// identical virtual-node layout and identical ownership for every key.
func TestAddOrderDoesNotMatter(t *testing.T) {
	ids := nodeIDs(10)
	keys := Keys(20000)

	var refPoints []Point
	var refOwners []string
	for p := 0; p < 20; p++ {
		perm := append([]string(nil), ids...)
		rng := rand.New(rand.NewSource(int64(p + 1)))
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

		r := buildRing(t, perm, 150)
		pts := r.Points()
		own := owners(t, r, keys)

		if p == 0 {
			refPoints, refOwners = pts, own
			continue
		}
		if len(pts) != len(refPoints) {
			t.Fatalf("perm %d: %d points, want %d", p, len(pts), len(refPoints))
		}
		for i := range pts {
			if pts[i] != refPoints[i] {
				t.Fatalf("perm %d: point %d is %+v, want %+v", p, i, pts[i], refPoints[i])
			}
		}
		for i := range own {
			if own[i] != refOwners[i] {
				t.Fatalf("perm %d: key %q owned by %q, want %q", p, keys[i], own[i], refOwners[i])
			}
		}
	}
}

// Rebuilding the same ring twice within one process must give identical
// ownership for every key. This guards against randomized hashing (e.g.
// maphash with a random seed), which would differ across instances.
func TestRebuildInSameProcessIsDeterministic(t *testing.T) {
	ids := nodeIDs(10)
	keys := Keys(20000)

	r1 := buildRing(t, ids, 150)
	r2 := buildRing(t, ids, 150)
	o1 := owners(t, r1, keys)
	o2 := owners(t, r2, keys)
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Fatalf("key %q owned by %q in first ring, %q in second", keys[i], o1[i], o2[i])
		}
	}

	p1, p2 := r1.Points(), r2.Points()
	if len(p1) != len(p2) {
		t.Fatalf("point counts differ: %d vs %d", len(p1), len(p2))
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Fatalf("point %d differs between rebuilds: %+v vs %+v", i, p1[i], p2[i])
		}
	}
}
