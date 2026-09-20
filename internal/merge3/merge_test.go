package merge3_test

import (
	"slices"
	"testing"

	"ontology/internal/merge3"
)

func requireClean(t *testing.T, r merge3.Result, want []string) {
	t.Helper()
	if r.HasConflict() {
		t.Fatalf("unexpected conflicts: %+v", r.Conflicts)
	}
	if !slices.Equal(r.Lines, want) {
		t.Fatalf("Lines = %q, want %q", r.Lines, want)
	}
}

func requireOneConflict(t *testing.T, r merge3.Result, want merge3.Conflict) {
	t.Helper()
	if len(r.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one", r.Conflicts)
	}
	got := r.Conflicts[0]
	if got.At != want.At ||
		!slices.Equal(got.Base, want.Base) ||
		!slices.Equal(got.Ours, want.Ours) ||
		!slices.Equal(got.Theirs, want.Theirs) {
		t.Fatalf("conflict = %+v, want %+v", got, want)
	}
}

// Rule 1: a change made by only one side is adopted.
func TestMergeOneSidedChange(t *testing.T) {
	base := []string{"a", "b", "c"}

	r, err := merge3.Merge(base, []string{"a", "B", "c"}, base)
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "B", "c"})

	r, err = merge3.Merge(base, base, []string{"a", "B", "c"})
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "B", "c"})
}

// Rule 2: identical changes on both sides are not a conflict.
func TestMergeIdenticalChanges(t *testing.T) {
	base := []string{"a", "b", "c", "d"}

	r, err := merge3.Merge(base, []string{"a", "X", "d"}, []string{"a", "X", "d"})
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "X", "d"})

	// Both sides delete the same region.
	r, err = merge3.Merge(base, []string{"a", "d"}, []string{"a", "d"})
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "d"})
}

// Rule 3: different changes to the same lines conflict.
func TestMergeDifferentChangesConflict(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := merge3.Merge(base, []string{"a", "X", "c"}, []string{"a", "Y", "c"})
	if err != nil {
		t.Fatal(err)
	}
	requireCleanLines(t, r, []string{"a", "c"})
	requireOneConflict(t, r, merge3.Conflict{
		At: 1, Base: []string{"b"}, Ours: []string{"X"}, Theirs: []string{"Y"},
	})
}

// Rule 4: delete versus modify is a conflict, never a silent delete.
func TestMergeDeleteVersusModify(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := merge3.Merge(base, []string{"a", "c"}, []string{"a", "B", "c"})
	if err != nil {
		t.Fatal(err)
	}
	requireCleanLines(t, r, []string{"a", "c"})
	requireOneConflict(t, r, merge3.Conflict{
		At: 1, Base: []string{"b"}, Ours: nil, Theirs: []string{"B"},
	})
}

// Rule 5: delete versus untouched adopts the deletion.
func TestMergeDeleteVersusUntouched(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := merge3.Merge(base, []string{"a", "c"}, base)
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "c"})
}

// Rule 6: inserts at different anchors merge cleanly, ordered by anchor.
func TestMergeDisjointInserts(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "x", "b", "c"}
	theirs := []string{"a", "b", "y", "c"}
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	requireClean(t, r, []string{"a", "x", "b", "y", "c"})
}

// Rule 7: different inserts at the same anchor conflict.
func TestMergeSameAnchorInserts(t *testing.T) {
	base := []string{"a", "b"}
	r, err := merge3.Merge(base, []string{"a", "x", "b"}, []string{"a", "y", "b"})
	if err != nil {
		t.Fatal(err)
	}
	requireCleanLines(t, r, []string{"a", "b"})
	requireOneConflict(t, r, merge3.Conflict{
		At: 1, Base: nil, Ours: []string{"x"}, Theirs: []string{"y"},
	})
}

// requireCleanLines checks the non-conflicting merged lines only.
func requireCleanLines(t *testing.T, r merge3.Result, want []string) {
	t.Helper()
	if !slices.Equal(r.Lines, want) {
		t.Fatalf("Lines = %q, want %q", r.Lines, want)
	}
}
