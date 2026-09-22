package merge3

import "testing"

// Regression: an insertion whose anchor point coincides with the
// start of a replacement on the other side is an independent edit;
// it must merge cleanly and keep both sides' lines.
func TestMergeInsertAtReplacementStart(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	ins := []string{"a", "b", "X", "c", "d"} // insert X after b
	rep := []string{"a", "b", "C", "d"}      // replace c with C
	want := []string{"a", "b", "X", "C", "d"}
	checkClean(t, base, ins, rep, want)
	checkClean(t, base, rep, ins, want)
}

// Regression: an insertion anchored exactly at the end of a
// replacement on the other side is likewise independent.
func TestMergeInsertAtReplacementEnd(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	rep := []string{"a", "B", "c", "d"}      // replace b with B
	ins := []string{"a", "b", "X", "c", "d"} // insert X after b
	want := []string{"a", "B", "X", "c", "d"}
	checkClean(t, base, rep, ins, want)
	checkClean(t, base, ins, rep, want)
}

// Guard: an insertion strictly inside a replaced region on the
// other side is genuinely entangled and must still conflict.
func TestMergeInsertInsideReplacement(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	rep := []string{"a", "B", "d"}           // replace b,c with B
	ins := []string{"a", "b", "X", "c", "d"} // insert X between b and c
	r := mustMerge(t, base, rep, ins)
	if !r.HasConflicts() {
		t.Fatal("insertion strictly inside a replacement must conflict")
	}
	r = mustMerge(t, base, ins, rep)
	if !r.HasConflicts() {
		t.Fatal("insertion strictly inside a replacement must conflict")
	}
}
