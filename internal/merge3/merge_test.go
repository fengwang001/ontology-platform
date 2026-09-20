package merge3

import (
	"slices"
	"testing"
)

func mustMerge(t *testing.T, base, ours, theirs []string) Result {
	t.Helper()
	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	return res
}

func assertClean(t *testing.T, res Result, want []string) {
	t.Helper()
	if res.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
	if !slices.Equal(res.Lines, want) {
		t.Fatalf("Lines = %q, want %q", res.Lines, want)
	}
}

// Rule 1: only theirs changed -> take theirs.
func TestOnlyTheirsChanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	theirs := []string{"a", "B", "c"}
	res := mustMerge(t, base, base, theirs)
	assertClean(t, res, theirs)
}

// Rule 1: only ours changed -> take ours.
func TestOnlyOursChanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "B", "c"}
	res := mustMerge(t, base, ours, base)
	assertClean(t, res, ours)
}

// Rule 2: both sides made the same edit -> adopt it, no conflict.
func TestBothSidesSameEdit(t *testing.T) {
	base := []string{"a", "b", "c"}
	edit := []string{"a", "X", "Y", "c"}
	res := mustMerge(t, base, edit, edit)
	assertClean(t, res, edit)
}

// Rule 2: both sides deleted the same segment -> no conflict.
func TestBothSidesSameDeletion(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	edit := []string{"a", "d"}
	res := mustMerge(t, base, edit, edit)
	assertClean(t, res, edit)
}

// Rule 3: both sides changed the same lines differently -> conflict.
func TestBothSidesDifferentEdits(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "O", "c"}
	theirs := []string{"a", "T", "c"}
	res := mustMerge(t, base, ours, theirs)
	if !res.HasConflict() {
		t.Fatal("expected conflict")
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if !slices.Equal(c.Base, []string{"b"}) ||
		!slices.Equal(c.Ours, []string{"O"}) ||
		!slices.Equal(c.Theirs, []string{"T"}) {
		t.Fatalf("conflict content = %+v", c)
	}
	if !slices.Equal(res.Lines, []string{"a", "c"}) {
		t.Fatalf("Lines = %q", res.Lines)
	}
	if c.LineIndex != 1 {
		t.Fatalf("LineIndex = %d, want 1", c.LineIndex)
	}
}

// Rule 4: one side deletes, the other modifies -> conflict.
func TestDeleteVersusModify(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "c"}        // deleted "b"
	theirs := []string{"a", "B", "c"} // modified "b"
	res := mustMerge(t, base, ours, theirs)
	if !res.HasConflict() {
		t.Fatal("expected conflict for delete-vs-modify")
	}
	c := res.Conflicts[0]
	if len(c.Ours) != 0 {
		t.Fatalf("Ours = %q, want empty (deleting side)", c.Ours)
	}
	if !slices.Equal(c.Base, []string{"b"}) || !slices.Equal(c.Theirs, []string{"B"}) {
		t.Fatalf("conflict content = %+v", c)
	}
}

// Rule 5: one side deletes, the other is unchanged -> take the deletion.
func TestDeleteVersusUnchanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "c"}
	res := mustMerge(t, base, ours, base)
	assertClean(t, res, ours)

	res = mustMerge(t, base, base, ours)
	assertClean(t, res, ours)
}
