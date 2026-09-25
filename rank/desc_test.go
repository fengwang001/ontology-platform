package rank

import "testing"

// Descending ranking of [10,20,20,30] must produce value order
// 30,20,20,10 with RANK 1,2,2,4 and DENSE_RANK 1,2,2,3, and the tied
// 20s must still be ordered by ascending row ID.
func TestDescendingColumns(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: 10, ID: "a"},
		{Partition: strptr("p"), Value: 20, ID: "c"},
		{Partition: strptr("p"), Value: 20, ID: "b"},
		{Partition: strptr("p"), Value: 30, ID: "d"},
	}
	res := Rank(rows, Config{Descending: true})

	wantIDs := []string{"d", "b", "c", "a"}
	want := []want3{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	}
	assertColumns(t, res, want)
	for i, id := range wantIDs {
		if res.Rows[i].Row.ID != id {
			t.Fatalf("row %d: id = %q, want %q (ties keep ascending ID order)",
				i, res.Rows[i].Row.ID, id)
		}
	}
}

// The descending result must not be a plain reversal of the ascending
// result: with ties, reversing the ascending output would order tied
// rows by descending ID and misassign ROW_NUMBER.
func TestDescendingIsNotReversedAscending(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: 20, ID: "a"},
		{Partition: strptr("p"), Value: 10, ID: "b"},
		{Partition: strptr("p"), Value: 20, ID: "c"},
		{Partition: strptr("p"), Value: 30, ID: "d"},
	}
	asc := Rank(rows, Config{}).Rows
	desc := Rank(rows, Config{Descending: true}).Rows

	if len(asc) != len(desc) {
		t.Fatalf("length mismatch: %d vs %d", len(asc), len(desc))
	}

	// A "simple reversal" hypothesis: desc[i] equals asc[n-1-i] in both
	// row identity and all three ranking columns. It must fail somewhere.
	n := len(asc)
	looksReversed := true
	for i := 0; i < n; i++ {
		a, d := asc[n-1-i], desc[i]
		if a.Row != d.Row || a.RowNumber != d.RowNumber ||
			a.Rank != d.Rank || a.DenseRank != d.DenseRank {
			looksReversed = false
			break
		}
	}
	if looksReversed {
		t.Fatal("descending output is a plain reversal of ascending output")
	}

	// Concretely: the tied 20-rows keep ascending ID order (a before c)
	// even in descending mode; a reversal would put c before a.
	if desc[1].Row.ID != "a" || desc[2].Row.ID != "c" {
		t.Fatalf("tied rows not in ascending ID order: %q, %q",
			desc[1].Row.ID, desc[2].Row.ID)
	}
	// And RANK still skips after the tie: the lowest value gets rank 4.
	if desc[3].Rank != 4 || desc[3].DenseRank != 3 {
		t.Fatalf("last row: Rank=%d DenseRank=%d, want 4 and 3",
			desc[3].Rank, desc[3].DenseRank)
	}
}
