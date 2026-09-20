package merge3

import (
	"reflect"
	"testing"
)

func mustMerge(t *testing.T, base, ours, theirs []string) Result {
	t.Helper()
	r, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	return r
}

func checkClean(t *testing.T, base, ours, theirs, want []string) {
	t.Helper()
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflicts() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	if !reflect.DeepEqual(r.Lines, want) {
		t.Fatalf("Lines = %q, want %q", r.Lines, want)
	}
}

// Rule 1: only one side changed -> take that side.
func TestMergeOnlyOneSideChanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	checkClean(t, base, base, []string{"a", "B", "c"}, []string{"a", "B", "c"})
	checkClean(t, base, []string{"a", "B", "c"}, base, []string{"a", "B", "c"})
}

// Rule 2: both sides made the identical change -> not a conflict.
func TestMergeBothSidesSameChange(t *testing.T) {
	base := []string{"a", "b", "c"}
	both := []string{"a", "X", "c"}
	checkClean(t, base, both, both, both)
	// Both sides deleted the same segment.
	checkClean(t, base, []string{"a"}, []string{"a"}, []string{"a"})
}

// Rule 3: both sides changed the same lines differently -> conflict.
func TestMergeBothSidesDiffer(t *testing.T) {
	base := []string{"a", "b", "c"}
	r := mustMerge(t, base, []string{"a", "X", "c"}, []string{"a", "Y", "c"})
	if !r.HasConflicts() {
		t.Fatal("expected a conflict")
	}
	if len(r.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(r.Conflicts))
	}
	c := r.Conflicts[0]
	if !reflect.DeepEqual(c.Ours, []string{"X"}) ||
		!reflect.DeepEqual(c.Base, []string{"b"}) ||
		!reflect.DeepEqual(c.Theirs, []string{"Y"}) {
		t.Fatalf("conflict content = %+v", c)
	}
}

// Rule 4: one side deletes, the other modifies -> conflict.
func TestMergeDeleteVersusModify(t *testing.T) {
	base := []string{"a", "b", "c"}
	r := mustMerge(t, base, []string{"a", "c"}, []string{"a", "B", "c"})
	if !r.HasConflicts() {
		t.Fatal("delete vs modify must conflict")
	}
	c := r.Conflicts[0]
	if len(c.Ours) != 0 || !reflect.DeepEqual(c.Theirs, []string{"B"}) {
		t.Fatalf("conflict content = %+v", c)
	}
}

// Rule 5: one side deletes, the other is unchanged -> take deletion.
func TestMergeDeleteVersusUnchanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	checkClean(t, base, []string{"a", "c"}, base, []string{"a", "c"})
	checkClean(t, base, base, []string{"a", "c"}, []string{"a", "c"})
}

// Rule 6: insertions at distinct anchor points -> both kept, ordered.
func TestMergeDisjointInsertions(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "ins-ours", "b", "c"}
	theirs := []string{"a", "b", "c", "ins-theirs"}
	want := []string{"a", "ins-ours", "b", "c", "ins-theirs"}
	checkClean(t, base, ours, theirs, want)
}

// Rule 7: both sides insert different content at the same point.
func TestMergeSamePointInsertions(t *testing.T) {
	base := []string{"a", "b"}
	r := mustMerge(t, base, []string{"a", "X", "b"}, []string{"a", "Y", "b"})
	if !r.HasConflicts() {
		t.Fatal("same-point different insertions must conflict")
	}
	c := r.Conflicts[0]
	if !reflect.DeepEqual(c.Ours, []string{"X"}) ||
		!reflect.DeepEqual(c.Theirs, []string{"Y"}) ||
		len(c.Base) != 0 {
		t.Fatalf("conflict content = %+v", c)
	}
	// Identical insertions at the same point merge cleanly.
	both := []string{"a", "X", "b"}
	checkClean(t, base, both, both, both)
}

// Adjacent but non-overlapping edits stay two independent results.
func TestMergeAdjacentNonOverlappingEdits(t *testing.T) {
	base := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7"}
	ours := []string{"l1", "l2", "O3", "l4", "l5", "l6", "l7"}
	theirs := []string{"l1", "l2", "l3", "l4", "l5", "l6", "T7"}
	want := []string{"l1", "l2", "O3", "l4", "l5", "l6", "T7"}
	checkClean(t, base, ours, theirs, want)
}

// Conflict blocks are minimal: shared head/tail lines of a changed
// region are merged normally, only the divergent middle conflicts.
func TestMergeConflictMinimality(t *testing.T) {
	base := []string{"head", "m1", "m2", "tail"}
	ours := []string{"same-head", "O1", "O2", "same-tail"}
	theirs := []string{"same-head", "T1", "T2", "same-tail"}
	r := mustMerge(t, base, ours, theirs)
	if len(r.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(r.Conflicts))
	}
	c := r.Conflicts[0]
	if !reflect.DeepEqual(c.Ours, []string{"O1", "O2"}) ||
		!reflect.DeepEqual(c.Theirs, []string{"T1", "T2"}) {
		t.Fatalf("conflict not minimal: %+v", c)
	}
	if !reflect.DeepEqual(r.Lines, []string{"same-head", "same-tail"}) {
		t.Fatalf("Lines = %q", r.Lines)
	}
	if c.Line != 1 {
		t.Fatalf("conflict Line = %d, want 1", c.Line)
	}
}

// Required property: Merge(base, x, x) == x with zero conflicts.
func TestMergeIdenticalSides(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		{},
		base,
		{"a", "b", "c", "d", "e"},
		{"x", "y"},
		{"a", "c"},
	}
	for _, x := range cases {
		checkClean(t, base, x, x, x)
	}
}

// Required property: Merge(base, base, y) == y with zero conflicts.
func TestMergeBaseUnchanged(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		{},
		base,
		{"a", "B", "c", "d", "e"},
		{"z"},
	}
	for _, y := range cases {
		checkClean(t, base, base, y, y)
		checkClean(t, base, y, base, y)
	}
}
