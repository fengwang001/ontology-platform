package ontology

import "testing"

// TestDescending checks that descending mode keeps the same tie rules
// and still breaks ties by ID ascending, and that the result is not a
// plain reversal of the ascending result.
func TestDescending(t *testing.T) {
	rows := makeRows(10, 20, 20, 30, 20)
	asc := Compute(rows, Options{})
	desc := Compute(rows, Options{Descending: true})

	// Descending: values 30,20,20,20,10. The 20-tie keeps IDs r2,r3,r5
	// ascending; RANK 1,2,2,2,5; DENSE_RANK 1,2,2,2,3.
	assertRanks(t, desc.Rows, []wantRank{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
	wantIDs := []string{"r4", "r2", "r3", "r5", "r1"}
	for i, id := range wantIDs {
		if desc.Rows[i].Row.ID != id {
			t.Fatalf("desc order[%d] = %s, want %s", i, desc.Rows[i].Row.ID, id)
		}
	}

	// Not a simple reversal of the ascending output: reversing asc
	// would order the 20-tie as r5,r3,r2 with mirrored rank numbers.
	if rowsEqual(asc.Rows, desc.Rows) {
		t.Fatal("descending output equals ascending output")
	}
	reversed := make([]RankedRow, len(asc.Rows))
	for i, r := range asc.Rows {
		reversed[len(asc.Rows)-1-i] = r
	}
	if rowsEqual(reversed, desc.Rows) {
		t.Fatal("descending output is a plain reversal of ascending output")
	}

	// Tie internals stay ID-ascending in descending mode.
	var tieIDs []string
	for _, r := range desc.Rows {
		if r.Row.Value == 20 {
			tieIDs = append(tieIDs, r.Row.ID)
		}
	}
	wantTie := []string{"r2", "r3", "r5"}
	for i, id := range wantTie {
		if tieIDs[i] != id {
			t.Fatalf("descending tie order = %v, want %v", tieIDs, wantTie)
		}
	}
}
