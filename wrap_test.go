package ontology

import (
	"strconv"
	"testing"
)

// findKeysInZones returns three keys whose hashes fall (1) before the
// smallest point, (2) strictly between the two points, (3) past the
// largest point of a two-vnode ring.
func findKeysInZones(t *testing.T, lo, hi Point) (before, between, after string) {
	t.Helper()
	for i := 0; ; i++ {
		k := "wrap-" + strconv.Itoa(i)
		h := HashKey(k)
		switch {
		case h < lo.Hash && before == "":
			before = k
		case h > lo.Hash && h < hi.Hash && between == "":
			between = k
		case h > hi.Hash && after == "":
			after = k
		}
		if before != "" && between != "" && after != "" {
			return before, between, after
		}
	}
}

// Wrap-around semantics: a key hashing past the largest virtual node must
// wrap to the smallest one (not error, not clamp to the largest).
func TestWrapAround(t *testing.T) {
	r := buildRing(t, []string{"alpha", "beta"}, 1)
	pts := r.Points()
	if len(pts) != 2 {
		t.Fatalf("want 2 points, got %d", len(pts))
	}
	lo, hi := pts[0], pts[1]
	before, between, after := findKeysInZones(t, lo, hi)
	cases := []struct {
		name, key, want string
	}{
		{"hash below smallest point", before, lo.Node},
		{"hash between the two points", between, hi.Node},
		{"hash past largest point wraps to smallest", after, lo.Node},
	}
	for _, c := range cases {
		got, err := r.Locate(c.key)
		if err != nil {
			t.Fatalf("%s: Locate(%q): %v", c.name, c.key, err)
		}
		if got != c.want {
			t.Fatalf("%s: Locate(%q) = %q, want %q", c.name, c.key, got, c.want)
		}
	}
}
