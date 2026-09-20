package merge3_test

import (
	"slices"
	"testing"

	"ontology/internal/merge3"
)

// Adjacent but non-overlapping edits produce two independent results
// with zero conflicts.
func TestMergeAdjacentNonOverlappingEdits(t *testing.T) {
	base := []string{"1", "2", "3", "4", "5", "6", "7"}
	ours := []string{"1", "2", "three", "4", "5", "6", "7"}
	theirs := []string{"1", "2", "3", "4", "5", "6", "seven"}
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"1", "2", "three", "4", "5", "6", "seven"})
}

// Within one changed region, lines both sides agree on at the edges are
// stripped out of the conflict block.
func TestMergeConflictIsMinimal(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ours := []string{"a", "X", "Y", "d"}
	theirs := []string{"a", "X", "Z", "d"}
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	// The agreed line X is a normal merged line, not part of the
	// conflict.
	requireCleanLines(t, r, []string{"a", "X", "d"})
	requireOneConflict(t, r, merge3.Conflict{
		At: 2, Base: []string{"b", "c"}, Ours: []string{"Y"}, Theirs: []string{"Z"},
	})
}

// The agreed suffix is stripped out of the conflict block as well.
func TestMergeConflictStripsSuffix(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "Y", "X"}
	theirs := []string{"a", "Z", "X"}
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	requireCleanLines(t, r, []string{"a", "X"})
	requireOneConflict(t, r, merge3.Conflict{
		At: 1, Base: []string{"b", "c"}, Ours: []string{"Y"}, Theirs: []string{"Z"},
	})
}

// Invariant: merging identical sides yields exactly those lines and no
// conflicts.
func TestMergeIdenticalSides(t *testing.T) {
	base := []string{"a", "b", "c"}
	xs := [][]string{
		nil,
		{},
		{"a", "b", "c"},
		{"x"},
		{"a", "X", "c", "tail"},
		{"totally", "different", "lines"},
	}
	for _, x := range xs {
		r, err := merge3.Merge(base, x, x)
		if err != nil {
			t.Fatal(err)
		}
		if r.HasConflict() {
			t.Errorf("Merge(base, %q, %q): unexpected conflicts %+v", x, x, r.Conflicts)
		}
		if !slices.Equal(r.Lines, x) {
			t.Errorf("Merge(base, %q, %q): Lines = %q", x, x, r.Lines)
		}
	}
}

// Invariant: merging base against base and y yields exactly y and no
// conflicts.
func TestMergeBaseAgainstBase(t *testing.T) {
	base := []string{"a", "b", "c"}
	ys := [][]string{
		nil,
		{},
		{"a", "b", "c"},
		{"y"},
		{"a", "Y", "c", "tail"},
		{"totally", "different", "lines"},
	}
	for _, y := range ys {
		r, err := merge3.Merge(base, base, y)
		if err != nil {
			t.Fatal(err)
		}
		if r.HasConflict() {
			t.Errorf("Merge(base, base, %q): unexpected conflicts %+v", y, r.Conflicts)
		}
		if !slices.Equal(r.Lines, y) {
			t.Errorf("Merge(base, base, %q): Lines = %q", y, r.Lines)
		}
		// Symmetric case: ours changed, theirs is base.
		r, err = merge3.Merge(base, y, base)
		if err != nil {
			t.Fatal(err)
		}
		if r.HasConflict() || !slices.Equal(r.Lines, y) {
			t.Errorf("Merge(base, %q, base): Lines = %q, conflicts = %+v", y, r.Lines, r.Conflicts)
		}
	}
}
