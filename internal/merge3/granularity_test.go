package merge3

import (
	"slices"
	"testing"
)

// Rule 6: insertions at different anchors merge cleanly, ordered by anchor.
func TestInsertionsAtDifferentAnchors(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "O1", "b", "c"}   // insert after "a"
	theirs := []string{"a", "b", "c", "T1"} // insert after "c"
	res := mustMerge(t, base, ours, theirs)
	assertClean(t, res, []string{"a", "O1", "b", "c", "T1"})
}

// Rule 7: different insertions at the same position -> conflict.
func TestInsertionsAtSameAnchor(t *testing.T) {
	base := []string{"a", "b"}
	ours := []string{"a", "O1", "b"}
	theirs := []string{"a", "T1", "b"}
	res := mustMerge(t, base, ours, theirs)
	if !res.HasConflict() {
		t.Fatal("expected conflict for competing insertions")
	}
	c := res.Conflicts[0]
	if len(c.Base) != 0 {
		t.Fatalf("Base = %q, want empty for pure insertion conflict", c.Base)
	}
	if !slices.Equal(c.Ours, []string{"O1"}) || !slices.Equal(c.Theirs, []string{"T1"}) {
		t.Fatalf("conflict content = %+v", c)
	}
}

// Adjacent but non-overlapping edits produce two clean results, no conflict.
func TestAdjacentNonOverlappingEdits(t *testing.T) {
	base := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	ours := []string{"l1", "l2", "O3", "l4", "l5", "l6", "l7", "l8", "l9"}
	theirs := []string{"l1", "l2", "l3", "l4", "l5", "l6", "T7", "l8", "l9"}
	res := mustMerge(t, base, ours, theirs)
	assertClean(t, res, []string{"l1", "l2", "O3", "l4", "l5", "l6", "T7", "l8", "l9"})
}

// Conflict blocks are minimal: identical leading/trailing lines of a changed
// region are stripped out as ordinary merged lines.
func TestConflictBlockIsMinimal(t *testing.T) {
	base := []string{"head", "x", "tail"}
	ours := []string{"head", "same1", "O", "same2", "tail"}
	theirs := []string{"head", "same1", "T", "same2", "tail"}
	res := mustMerge(t, base, ours, theirs)
	if len(res.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if !slices.Equal(c.Ours, []string{"O"}) || !slices.Equal(c.Theirs, []string{"T"}) {
		t.Fatalf("conflict not minimal: %+v", c)
	}
	if !slices.Equal(res.Lines, []string{"head", "same1", "same2", "tail"}) {
		t.Fatalf("Lines = %q", res.Lines)
	}
	if c.LineIndex != 2 {
		t.Fatalf("LineIndex = %d, want 2", c.LineIndex)
	}
}

// Two separated conflicts stay two independent minimal blocks.
func TestTwoIndependentConflicts(t *testing.T) {
	base := []string{"a", "b", "c", "d", "e"}
	ours := []string{"a", "O1", "c", "O2", "e"}
	theirs := []string{"a", "T1", "c", "T2", "e"}
	res := mustMerge(t, base, ours, theirs)
	if len(res.Conflicts) != 2 {
		t.Fatalf("got %d conflicts, want 2", len(res.Conflicts))
	}
	if !slices.Equal(res.Lines, []string{"a", "c", "e"}) {
		t.Fatalf("Lines = %q", res.Lines)
	}
	if res.Conflicts[0].LineIndex != 1 || res.Conflicts[1].LineIndex != 2 {
		t.Fatalf("LineIndexes = %d, %d", res.Conflicts[0].LineIndex, res.Conflicts[1].LineIndex)
	}
}

// Invariant: Merge(base, x, x) == x with zero conflicts, for any x.
func TestMergeIdenticalSides(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		nil,
		{},
		{"a", "b", "c", "d"},
		{"x", "y"},
		{"a", "a", "a"},
		{"d", "c", "b", "a"},
		{"new1", "a", "new2", "new3", "d", "new4"},
	}
	for _, x := range cases {
		res := mustMerge(t, base, x, x)
		if res.HasConflict() {
			t.Fatalf("Merge(base, %q, %q) has conflicts", x, x)
		}
		if !slices.Equal(res.Lines, x) {
			t.Fatalf("Merge(base, %q, %q).Lines = %q", x, x, res.Lines)
		}
	}
}

// Invariant: Merge(base, base, y) == y with zero conflicts, for any y.
func TestMergeBaseEqualsOurs(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		nil,
		{},
		{"a", "b", "c", "d"},
		{"x", "y"},
		{"a", "a", "a"},
		{"new1", "a", "new2", "new3", "d", "new4"},
	}
	for _, y := range cases {
		res := mustMerge(t, base, base, y)
		if res.HasConflict() {
			t.Fatalf("Merge(base, base, %q) has conflicts", y)
		}
		if !slices.Equal(res.Lines, y) {
			t.Fatalf("Merge(base, base, %q).Lines = %q", y, res.Lines)
		}
	}
}
