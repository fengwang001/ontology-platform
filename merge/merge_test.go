package merge

import (
	"math/rand"
	"sort"
	"strconv"
	"testing"

	"ontology/snap"
)

// randRows builds n rows with distinct keys inside a sorted keySpace, then
// returns them strictly sorted with random values.
func randRows(rng *rand.Rand, n int, keySpace int64) []snap.Row {
	seen := make(map[int64]bool, n)
	out := make([]snap.Row, 0, n)
	for len(out) < n {
		k := rng.Int63n(keySpace)
		if !seen[k] {
			seen[k] = true
			out = append(out, snap.Row{Key: k, Val: strconv.Itoa(rng.Intn(4))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Table-driven: across several (n,m) tiers with random sorted inputs the
// comparison count must stay linear (<= n+m), never n*m.
func TestComparisonCountBounds(t *testing.T) {
	cases := []struct{ n, m int }{
		{100, 100}, {100, 1000}, {1000, 500}, {5000, 5000}, {10000, 100},
	}
	for _, c := range cases {
		rng := rand.New(rand.NewSource(int64(c.n*31 + c.m)))
		for trial := 0; trial < 5; trial++ {
			o := randRows(rng, c.n, int64(c.n)*3+int64(c.m))
			nw := randRows(rng, c.m, int64(c.n)*3+int64(c.m))
			var d differ
			d.run(o, nw)
			if d.cmp > c.n+c.m {
				t.Fatalf("n=%d m=%d trial=%d: %d comparisons > n+m=%d",
					c.n, c.m, trial, d.cmp, c.n+c.m)
			}
			if d.cmp <= 0 {
				t.Fatalf("n=%d m=%d: expected comparisons, got 0", c.n, c.m)
			}
		}
	}
}

// old = 1..n, new = n+1..n+m (disjoint, all old first): every step compares
// old<new for n steps, then the old side is exhausted and the m tail inserts
// compare for free. Count must be exactly n.
func TestDisjointComparisonCount(t *testing.T) {
	cases := []struct{ n, m int64 }{
		{1, 1}, {100, 100}, {500, 1000}, {1000, 10000}, {10000, 333},
	}
	for _, c := range cases {
		var d differ
		d.run(span(1, c.n), span(c.n+1, c.n+c.m))
		if int64(d.cmp) != c.n {
			t.Fatalf("n=%d m=%d: comparisons=%d, want exactly %d",
				c.n, c.m, d.cmp, c.n)
		}
	}
}

// Section-3 worked example: 7 comparisons, exact 7-entry changelog.
func TestSectionThree(t *testing.T) {
	r := func(k int64, v string) snap.Row { return snap.Row{Key: k, Val: v} }
	o := []snap.Row{r(1, "a"), r(3, "b"), r(4, "c"), r(7, "d"), r(9, "e")}
	nw := []snap.Row{r(2, "x"), r(3, "b"), r(4, "C"), r(5, "y"),
		r(9, "e"), r(10, "z"), r(12, "w")}
	want := []Change{{'D', 1, "a", ""}, {'I', 2, "", "x"}, {'U', 4, "c", "C"},
		{'I', 5, "", "y"}, {'D', 7, "d", ""}, {'I', 10, "", "z"}, {'I', 12, "", "w"}}
	var d differ
	if got := d.run(o, nw); d.cmp != 7 || len(got) != len(want) {
		t.Fatalf("cmp=%d got=%v", d.cmp, got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("change %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	}
}
