package ontology

import "testing"

// When two virtual nodes collide on the same hash, the lexicographically
// smaller node ID wins, regardless of the order points were inserted.
func TestHashCollisionLexicographicTieBreak(t *testing.T) {
	const h = uint64(1 << 40)

	r1 := &Ring{vnodes: map[string]int{}, points: []point{
		{hash: h, node: "node-b"},
		{hash: h, node: "node-a"},
		{hash: h + 100, node: "node-c"},
	}}
	sortPoints(r1.points)

	// Same points, inserted in a different order.
	r2 := &Ring{vnodes: map[string]int{}, points: []point{
		{hash: h + 100, node: "node-c"},
		{hash: h, node: "node-a"},
		{hash: h, node: "node-b"},
	}}
	sortPoints(r2.points)

	if len(r1.points) != len(r2.points) {
		t.Fatalf("point counts differ: %d vs %d", len(r1.points), len(r2.points))
	}
	for i := range r1.points {
		if r1.points[i] != r2.points[i] {
			t.Fatalf("insertion order changed layout at %d: %+v vs %+v",
				i, r1.points[i], r2.points[i])
		}
	}

	for _, r := range []*Ring{r1, r2} {
		if got := r.locateHash(h); got != "node-a" {
			t.Fatalf("locateHash(collision) = %q, want lexicographically smallest %q", got, "node-a")
		}
		if got := r.locateHash(h - 1); got != "node-a" {
			t.Fatalf("locateHash(h-1) = %q, want %q", got, "node-a")
		}
		if got := r.locateHash(h + 1); got != "node-c" {
			t.Fatalf("locateHash(h+1) = %q, want %q", got, "node-c")
		}
		if got := r.locateHash(h + 200); got != "node-a" {
			t.Fatalf("locateHash past max = %q, want wrap to %q", got, "node-a")
		}
	}
}
