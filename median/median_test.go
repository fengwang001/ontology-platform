package median

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/wmid"
)

// naiveRef is the deliberately simple O(m log m) reference definition.
func naiveRef(elems [][2]int64) int64 {
	s := make([][2]int64, len(elems))
	copy(s, elems)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var w int64
	for _, e := range s {
		w += e[1]
	}
	var p int64
	for _, e := range s {
		p += e[1]
		if 2*p >= w {
			return e[0]
		}
	}
	return s[len(s)-1][0]
}

func TestFinderAgainstNaive(t *testing.T) {
	cases := [][][2]int64{
		{{30, 3}, {10, 3}, {40, 3}, {20, 3}}, // NOTES canonical -> 20
		{{1, 1}},
		{{5, 100}, {1, 1}, {9, 1}, {-3, 1}, {7, 7}},
		{{10, 1}, {20, 1}, {30, 1}, {40, 1}, {50, 1}, {60, 1}},
	}
	for i, seq := range cases {
		var f Finder
		for _, e := range seq {
			if err := f.Insert(e[0], e[1]); err != nil {
				t.Fatalf("case %d: insert %v: %v", i, e, err)
			}
		}
		got, err := f.Median()
		if err != nil {
			t.Fatalf("case %d: median: %v", i, err)
		}
		if want := naiveRef(seq); got != want {
			t.Fatalf("case %d: got %d want %d", i, got, want)
		}
	}
}

func TestEmptyAndReject(t *testing.T) {
	var f Finder
	if _, err := f.Median(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty median err = %v, want ErrEmpty", err)
	}
	if err := f.Insert(1, 0); !errors.Is(err, wmid.ErrNonPositiveWeight) {
		t.Fatalf("weight 0 err = %v", err)
	}
	if err := f.Insert(1, -3); !errors.Is(err, wmid.ErrNonPositiveWeight) {
		t.Fatalf("negative weight err = %v", err)
	}
	if err := f.Insert(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := f.Insert(1, 2); !errors.Is(err, wmid.ErrDuplicateValue) {
		t.Fatalf("duplicate err = %v", err)
	}
	if f.Total() != 2 {
		t.Fatalf("total after rejects = %d, want 2", f.Total())
	}
}

// TestTraversalBound pins O(log m) lookup: after inserting m elements one
// Median call visits at most 2*ceil(log2(m))+2 nodes, never all m. It
// reads the unexported counter from inside the package (white box); no
// exported method exposes the count.
func TestTraversalBound(t *testing.T) {
	sizes := []int{100, 300, 500, 1000, 3000, 5000, 10000}
	r := rand.New(rand.NewSource(7))
	for _, m := range sizes {
		var f Finder
		for _, v := range r.Perm(m) { // random insertion order
			if err := f.Insert(int64(v), int64(1+(v%7))); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.Median(); err != nil {
			t.Fatal(err)
		}
		bound := int64(2*ceilLog2(m) + 2)
		steps := f.steps.Load()
		if steps > bound {
			t.Fatalf("m=%d: steps=%d exceeds bound=%d", m, steps, bound)
		}
		if steps >= int64(m) {
			t.Fatalf("m=%d: steps=%d is not sublinear (>= m)", m, steps)
		}
	}
}
