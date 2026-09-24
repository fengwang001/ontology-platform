package ontology

import "testing"

// When two vnodes share a position, the lexicographically smaller node ID
// owns it regardless of the order the entries were supplied.
func TestCollisionLexicographicTieBreak(t *testing.T) {
	const pos = uint64(1) << 63

	below, _ := findKey(t, func(h uint64) bool { return h < pos })
	above, _ := findKey(t, func(h uint64) bool { return h > pos })

	orders := [][]vnode{
		{{pos: pos, node: "zzz"}, {pos: pos, node: "aaa"}},
		{{pos: pos, node: "aaa"}, {pos: pos, node: "zzz"}},
	}
	for i, entries := range orders {
		ring := newHandcraftedRing(entries)
		if ring.ring[0].node != "aaa" {
			t.Fatalf("order %d: tie winner is %q, want aaa", i, ring.ring[0].node)
		}
		for _, key := range []string{below, above} {
			got, err := ring.Locate(key)
			if err != nil {
				t.Fatalf("order %d: Locate: %v", i, err)
			}
			if got != "aaa" {
				t.Fatalf("order %d, key %q: got %q, want aaa", i, key, got)
			}
		}
}

// Tie-breaking must hold for real hashed vnodes too: build rings in both
// insertion orders for the same pair of nodes and compare layouts.
func TestCollisionTieBreakIndependentOfAddOrder(t *testing.T) {
	idsA := []string{"node-alpha", "node-beta"}
	idsB := []string{"node-beta", "node-alpha"}

	r1 := buildRing(t, idsA, 200)
	r2 := buildRing(t, idsB, 200)

	if len(r1.ring) != len(r2.ring) {
		t.Fatalf("layout length mismatch: %d != %d", len(r1.ring), len(r2.ring))
	}
	for i := range r1.ring {
		if r1.ring[i] != r2.ring[i] {
			t.Fatalf("layout differs at %d: %+v != %+v", i, r1.ring[i], r2.ring[i])
		}
	}
}
