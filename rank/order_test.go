package rank

import (
	"fmt"
	"testing"
)

// permutations returns every permutation of indices [0..n).
func permutations(n int) [][]int {
	var out [][]int
	var walk func([]int, int)
	walk = func(p []int, k int) {
		if k == len(p) {
			cp := make([]int, len(p))
			copy(cp, p)
			out = append(out, cp)
			return
		}
		for i := k; i < len(p); i++ {
			p[k], p[i] = p[i], p[k]
			walk(p, k+1)
			p[k], p[i] = p[i], p[k]
		}
	}
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	walk(p, 0)
	return out
}

// Across 24 input orderings (>= 20 required), output must be byte-for-byte
// identical including ROW_NUMBER and the IDs inside tie groups.
func TestTieOrderStableAcrossPermutations(t *testing.T) {
	base := makeRows(10, 20, 20, 30)
	perms := permutations(len(base))
	if len(perms) < 20 {
		t.Fatalf("need >= 20 permutations, got %d", len(perms))
	}

	var reference []RankedRow
	for pi, perm := range perms {
		shuffled := make([]Row, len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		got := Rank(shuffled, Asc).Rows
		if pi == 0 {
			reference = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(reference) {
			t.Fatalf("permutation %v:\n got  %v\n want %v", perm, got, reference)
		}
	}

	// The tied 20 rows must appear ID-ascending: b (rn 2) before c (rn 3).
	if reference[1].ID != "b" || reference[2].ID != "c" {
		t.Fatalf("tie order not ID-ascending: %s before %s", reference[1].ID, reference[2].ID)
	}
}
