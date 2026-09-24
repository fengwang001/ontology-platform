package ontology

import (
	"math/rand"
	"testing"
)

// buildRing adds the given node IDs in order, all with vnodes replicas.
func buildRing(t *testing.T, order []string, vnodes int) *Ring {
	t.Helper()
	r := New()
	for _, id := range order {
		if err := r.Add(id, vnodes); err != nil {
			t.Fatalf("add %q: %v", id, err)
		}
	}
	return r
}

// Two independently constructed rings must have identical vnode layouts and
// per-key owners regardless of insertion order.
func TestInsertionOrderDeterminism(t *testing.T) {
	const nodeCount, vnodes = 10, 200

	base := make([]string, nodeCount)
	for i := range base {
		base[i] = nodeName(i)
	}
	keys := generateKeys(10000) // layout comparison carries the real weight

	rng := rand.New(rand.NewSource(1))
	reference := buildRing(t, base, vnodes)
	refLayout := append([]vnode(nil), reference.ring...)
	refOwners := locateAll(reference, keys)

	for p := 0; p < 20; p++ {
		order := append([]string(nil), base...)
		rng.Shuffle(len(order), func(i, j int) {
			order[i], order[j] = order[j], order[i]
		})
		r := buildRing(t, order, vnodes)

		if len(r.ring) != len(refLayout) {
			t.Fatalf("permutation %d: vnode count %d != %d",
				p, len(r.ring), len(refLayout))
		}
		for i, vn := range refLayout {
			if r.ring[i] != vn {
				t.Fatalf("permutation %d: layout differs at index %d: %+v != %+v",
					p, i, r.ring[i], vn)
			}
		}
		owners := locateAll(r, keys)
		for i := range keys {
			if owners[i] != refOwners[i] {
				t.Fatalf("permutation %d: key %d owner %q != %q",
					p, i, owners[i], refOwners[i])
			}
		}
	}
}

// Rebuilding the same ring in the same process must not change anything: the
// hash has no process-local random seed.
func TestSameProcessRebuild(t *testing.T) {
	keys := generateKeys(5000)
	build := func() []string {
		r := New()
		for i := 0; i < 8; i++ {
			if err := r.Add(nodeName(i), 150); err != nil {
				t.Fatalf("add: %v", err)
			}
		}
		return locateAll(r, keys)
	}

	first, second := build(), build()
	for i := range keys {
		if first[i] != second[i] {
			t.Fatalf("rebuild changed key %d owner %q -> %q",
				i, first[i], second[i])
		}
	}
}
