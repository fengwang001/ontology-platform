package merge3

import (
	"slices"
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

func TestOnlyTheirsChanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "b", "c"}
	theirs := []string{"a", "B", "c", "d"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, theirs) {
		t.Fatalf("got %v, want %v", r.Lines, theirs)
	}
}

func TestOnlyOursChanged(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "B"}
	theirs := []string{"a", "b", "c"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, ours) {
		t.Fatalf("got %v, want %v", r.Lines, ours)
	}
}

func TestBothSameChange(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "X", "c"}
	theirs := []string{"a", "X", "c"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("identical changes must not conflict: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, ours) {
		t.Fatalf("got %v, want %v", r.Lines, ours)
	}
}

func TestBothDeleteSameRange(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ours := []string{"a", "d"}
	theirs := []string{"a", "d"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("identical deletions must not conflict: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, ours) {
		t.Fatalf("got %v, want %v", r.Lines, ours)
	}
}

func TestDifferentChangesConflict(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "O", "c"}
	theirs := []string{"a", "T", "c"}
	r := mustMerge(t, base, ours, theirs)
	if len(r.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if !slices.Equal(c.Ours, []string{"O"}) ||
		!slices.Equal(c.Theirs, []string{"T"}) ||
		!slices.Equal(c.Base, []string{"b"}) {
		t.Fatalf("bad conflict content: %+v", c)
	}
}

func TestDeleteVsModifyConflicts(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "c"}        // deleted b
	theirs := []string{"a", "B", "c"} // modified b
	r := mustMerge(t, base, ours, theirs)
	if len(r.Conflicts) != 1 {
		t.Fatalf("delete vs modify must conflict, got %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if len(c.Ours) != 0 || !slices.Equal(c.Theirs, []string{"B"}) {
		t.Fatalf("bad conflict content: %+v", c)
	}
}

func TestDeleteVsUntouched(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "c"} // deleted b
	theirs := []string{"a", "b", "c"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, ours) {
		t.Fatalf("got %v, want %v", r.Lines, ours)
	}
}

func TestSeparateInsertionsBothKept(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "O1", "b", "c"}   // insert before b
	theirs := []string{"a", "b", "c", "T1"} // insert after c
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	want := []string{"a", "O1", "b", "c", "T1"}
	if !slices.Equal(r.Lines, want) {
		t.Fatalf("got %v, want %v", r.Lines, want)
	}
}

func TestSamePositionInsertConflicts(t *testing.T) {
	base := []string{"a", "b"}
	ours := []string{"a", "O1", "b"}
	theirs := []string{"a", "T1", "b"}
	r := mustMerge(t, base, ours, theirs)
	if len(r.Conflicts) != 1 {
		t.Fatalf("same-position insertions must conflict, got %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if !slices.Equal(c.Ours, []string{"O1"}) ||
		!slices.Equal(c.Theirs, []string{"T1"}) || len(c.Base) != 0 {
		t.Fatalf("bad conflict content: %+v", c)
	}
}

func TestSamePositionIdenticalInsert(t *testing.T) {
	base := []string{"a", "b"}
	ours := []string{"a", "X", "b"}
	theirs := []string{"a", "X", "b"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("identical insertions must not conflict: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, ours) {
		t.Fatalf("got %v, want %v", r.Lines, ours)
	}
}

func TestAdjacentNonOverlappingEdits(t *testing.T) {
	base := []string{"1", "2", "3", "4", "5", "6", "7"}
	ours := []string{"1", "2", "O", "4", "5", "6", "7"}
	theirs := []string{"1", "2", "3", "4", "5", "6", "T"}
	r := mustMerge(t, base, ours, theirs)
	if r.HasConflict() {
		t.Fatalf("non-overlapping edits must not conflict: %+v", r.Conflicts)
	}
	want := []string{"1", "2", "O", "4", "5", "6", "T"}
	if !slices.Equal(r.Lines, want) {
		t.Fatalf("got %v, want %v", r.Lines, want)
	}
}

func TestConflictIsMinimal(t *testing.T) {
	base := []string{"m1", "m2"}
	ours := []string{"KEEP", "o", "TAIL"}
	theirs := []string{"KEEP", "t", "TAIL"}
	r := mustMerge(t, base, ours, theirs)
	if len(r.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if !slices.Equal(c.Ours, []string{"o"}) ||
		!slices.Equal(c.Theirs, []string{"t"}) {
		t.Fatalf("common edges must be stripped: %+v", c)
	}
	if c.Line != 1 {
		t.Fatalf("conflict should start at output line 1, got %d", c.Line)
	}
	want := []string{"KEEP", "o", "TAIL"}
	if !slices.Equal(r.Lines, want) {
		t.Fatalf("got %v, want %v", r.Lines, want)
	}
}
