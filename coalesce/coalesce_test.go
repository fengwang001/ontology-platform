package coalesce

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/rangespec"
)

func mustNormalize(t *testing.T, specs []rangespec.Spec, total int64) []Range {
	t.Helper()
	got, err := Normalize(specs, total)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return got
}

func TestClipRules(t *testing.T) {
	// a-b with b past the end is clipped to the last byte.
	got := mustNormalize(t, []rangespec.Spec{{First: 90, Last: 500, Suffix: -1}}, 100)
	if len(got) != 1 || got[0] != (Range{90, 99}) {
		t.Fatalf("a-b clip: %v", got)
	}
	// a- runs to the end of the resource.
	got = mustNormalize(t, []rangespec.Spec{{First: 90, Last: -1, Suffix: -1}}, 100)
	if len(got) != 1 || got[0] != (Range{90, 99}) {
		t.Fatalf("a- clip: %v", got)
	}
	// -n with n > total takes the whole resource.
	got = mustNormalize(t, []rangespec.Spec{{Suffix: 500}}, 100)
	if len(got) != 1 || got[0] != (Range{0, 99}) {
		t.Fatalf("-n clip: %v", got)
	}
	// -n with n <= total takes the last n bytes.
	got = mustNormalize(t, []rangespec.Spec{{Suffix: 10}}, 100)
	if len(got) != 1 || got[0] != (Range{90, 99}) {
		t.Fatalf("-n exact: %v", got)
	}
}

func TestUnsatisfiableCarriesTotal(t *testing.T) {
	cases := []struct {
		name  string
		specs []rangespec.Spec
	}{
		{"start past end", []rangespec.Spec{{First: 100, Last: -1, Suffix: -1}}},
		{"minus zero", []rangespec.Spec{{Suffix: 0}}},
		{"all dropped", []rangespec.Spec{{Suffix: 0}, {First: 500, Last: 600, Suffix: -1}}},
		{"empty resource", []rangespec.Spec{{First: 0, Last: -1, Suffix: -1}}},
	}
	for _, c := range cases {
		total := int64(100)
		if c.name == "empty resource" {
			total = 0
		}
		_, err := Normalize(c.specs, total)
		var ue *UnsatisfiableError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: not an UnsatisfiableError: %v", c.name, err)
		}
		if ue.Total != total {
			t.Fatalf("%s: Total=%d, want %d", c.name, ue.Total, total)
		}
	}
}

func TestMergeOverlappingAndAdjacent(t *testing.T) {
	specs := []rangespec.Spec{
		{First: 10, Last: 20, Suffix: -1},
		{First: 15, Last: 25, Suffix: -1}, // overlaps previous
		{First: 26, Last: 30, Suffix: -1}, // adjacent to merged 10-25
		{First: 50, Last: 60, Suffix: -1}, // disjoint
		{First: 0, Last: 5, Suffix: -1},   // sorts first
	}
	got := mustNormalize(t, specs, 100)
	want := []Range{{0, 5}, {10, 30}, {50, 60}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func expand(specs []rangespec.Spec, total int64) map[int64]bool {
	set := map[int64]bool{}
	for _, s := range specs {
		if r, ok := clip(s, total); ok {
			for b := r.First; b <= r.Last; b++ {
				set[b] = true
			}
		}
	}
	return set
}

// TestByteSetPreserved is the exhaustive cross-check for requirement 3:
// random spec sets are expanded to byte sets before and after Normalize and
// must be identical, and the merged list must be disjoint and non-adjacent.
func TestByteSetPreserved(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		total := int64(1 + rng.Intn(200))
		n := 1 + rng.Intn(30)
		specs := make([]rangespec.Spec, 0, n)
		for i := 0; i < n; i++ {
			switch rng.Intn(3) {
			case 0:
				a := int64(rng.Intn(int(total) + 5))
				b := a + int64(rng.Intn(int(total)+5))
				specs = append(specs, rangespec.Spec{First: a, Last: b, Suffix: -1})
			case 1:
				specs = append(specs, rangespec.Spec{First: int64(rng.Intn(int(total) + 5)), Last: -1, Suffix: -1})
			case 2:
				specs = append(specs, rangespec.Spec{Suffix: int64(rng.Intn(int(total) + 5))})
			}
		}
		before := expand(specs, total)
		got, err := Normalize(specs, total)
		if len(before) == 0 {
			if err == nil {
				t.Fatalf("trial %d: expected unsatisfiable", trial)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		after := map[int64]bool{}
		prev := Range{First: -2, Last: -2}
		for _, r := range got {
			if r.First <= prev.Last+1 {
				t.Fatalf("trial %d: ranges %v and %v overlap or touch", trial, prev, r)
			}
			for b := r.First; b <= r.Last; b++ {
				after[b] = true
			}
			prev = r
		}
		if len(before) != len(after) {
			t.Fatalf("trial %d: set size %d != %d", trial, len(before), len(after))
		}
		for b := range before {
			if !after[b] {
				t.Fatalf("trial %d: byte %d lost by merge", trial, b)
			}
		}
	}
}
