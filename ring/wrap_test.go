package ring

import "testing"

// handRing builds a ring with exactly two virtual nodes at positions
// lo and hi, owned by loNode and hiNode respectively.
func handRing(lo, hi uint64, loNode, hiNode string) *Ring {
	r := New()
	r.addPoint(lo, loNode)
	r.addPoint(hi, hiNode)
	return r
}

// Keys hashing past the largest virtual node must wrap to the smallest
// one; keys before the smallest land on it; keys in between land on the
// next position clockwise. All three cases are checked with real keys.
func TestWrapAroundSemantics(t *testing.T) {
	const lo = uint64(1) << 62
	const hi = uint64(1) << 63
	r := handRing(lo, hi, "alpha", "beta")

	before := findKeyWithHash(t, func(h uint64) bool { return h < lo })
	middle := findKeyWithHash(t, func(h uint64) bool { return h > lo && h < hi })
	after := findKeyWithHash(t, func(h uint64) bool { return h > hi })

	cases := []struct {
		name string
		key  string
		want string
	}{
		{"before smallest position", before, "alpha"},
		{"between the two positions", middle, "beta"},
		{"past largest position wraps", after, "alpha"},
	}
	for _, c := range cases {
		got, err := r.Locate(c.key)
		if err != nil {
			t.Fatalf("%s: Locate(%q): %v", c.name, c.key, err)
		}
		if got != c.want {
			t.Errorf("%s: Locate(%q) = %s, want %s (hash=%d)",
				c.name, c.key, got, c.want, HashKey(c.key))
		}
	}
}

// findKeyWithHash scans deterministically for a key whose ring position
// satisfies pred.
func findKeyWithHash(t *testing.T, pred func(uint64) bool) string {
	t.Helper()
	for i := 0; ; i++ {
		k := nodeID(i) + "-wrap"
		if pred(HashKey(k)) {
			return k
		}
		if i > 1<<30 {
			t.Fatal("no key found matching hash predicate")
		}
	}
}

// Two virtual nodes at the same position are tied by hash; the winner
// must be the lexicographically smaller node ID, regardless of the
// order in which the colliding points were inserted.
func TestCollisionTieBreakByNodeID(t *testing.T) {
	const pos = 424242

	r1 := New()
	r1.addPoint(pos, "node-b")
	r1.addPoint(pos, "node-a")

	r2 := New()
	r2.addPoint(pos, "node-a")
	r2.addPoint(pos, "node-b")

	for _, r := range []*Ring{r1, r2} {
		got, err := r.LocateHash(pos)
		if err != nil {
			t.Fatalf("LocateHash: %v", err)
		}
		if got != "node-a" {
			t.Fatalf("collision at %d resolved to %s, want node-a", pos, got)
		}
	}
}
