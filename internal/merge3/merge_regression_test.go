package merge3

import (
	"reflect"
	"testing"
)

// Regression: user report. Ours inserts X after b (point 2), theirs
// replaces c with C (range [2,3)). The insertion point coincides with
// the replacement's start but is not inside it, so the two edits must
// merge cleanly. Before the fix the point group swallowed the
// replacement (overlaps used a closed left bound), producing one
// conflict and dropping both X and C from Lines.
func TestRegressionInsertAtReplaceStart(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ours := []string{"a", "b", "X", "c", "d"}
	theirs := []string{"a", "b", "C", "d"}
	checkClean(t, base, ours, theirs, []string{"a", "b", "X", "C", "d"})
	// Mirrored: replacement on ours, insertion on theirs.
	checkClean(t, base, theirs, ours, []string{"a", "b", "X", "C", "d"})
}

// Regression: insertion exactly at the end boundary of a replacement
// (insert X after b while the other side replaces b). The insertion
// sits just past the replaced range and must merge cleanly. Before
// the fix the non-empty group absorbed the boundary insertion
// (overlaps used a closed right bound), causing a spurious conflict
// and lost lines.
func TestRegressionInsertAtReplaceEnd(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ours := []string{"a", "B", "c", "d"}
	theirs := []string{"a", "b", "X", "c", "d"}
	checkClean(t, base, ours, theirs, []string{"a", "B", "X", "c", "d"})
	// Mirrored: insertion on ours, replacement on theirs.
	checkClean(t, base, theirs, ours, []string{"a", "B", "X", "c", "d"})
}

// Guard against overcorrection: an insertion strictly inside a
// replaced region genuinely overlaps it and must still conflict.
func TestRegressionInsertInsideReplaceStillConflicts(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ours := []string{"a", "B", "C", "d"}        // replaces b,c -> B,C over [1,3)
	theirs := []string{"a", "b", "X", "c", "d"} // inserts X at point 2, strictly inside
	r := mustMerge(t, base, ours, theirs)
	if !r.HasConflicts() {
		t.Fatal("insertion strictly inside a replaced region must conflict")
	}
	if len(r.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(r.Conflicts))
	}
	c := r.Conflicts[0]
	if !reflect.DeepEqual(c.Ours, []string{"B", "C"}) ||
		!reflect.DeepEqual(c.Base, []string{"b", "c"}) ||
		!reflect.DeepEqual(c.Theirs, []string{"b", "X", "c"}) {
		t.Fatalf("conflict content = %+v", c)
	}
}
