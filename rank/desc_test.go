package rank

import "testing"

func ids(res Result) []int64 {
	out := make([]int64, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = r.Row.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDescendingSemantics checks desc ranking on [10,20,20,30]:
// tie rules for RANK/DENSE_RANK are unchanged, and ties still
// break by ID ascending.
func TestDescendingSemantics(t *testing.T) {
	res := Compute(rowsFromValues("p", []float64{10, 20, 20, 30}), Options{Desc: true})

	wantIDs := []int64{4, 2, 3, 1} // 30, then 20s by ID asc, then 10
	if got := ids(res); !equalIDs(got, wantIDs) {
		t.Fatalf("desc order: got ids %v, want %v", got, wantIDs)
	}
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

// TestDescendingIsNotReversedAscending proves the desc result is
// not produced by reversing the asc result: reversal would flip
// the in-tie ID order, which must stay ascending.
func TestDescendingIsNotReversedAscending(t *testing.T) {
	rows := rowsFromValues("p", []float64{10, 20, 20, 30})
	asc := Compute(rows, Options{})
	desc := Compute(rows, Options{Desc: true})

	reversed := make([]int64, 0, len(asc.Rows))
	for i := len(asc.Rows) - 1; i >= 0; i-- {
		reversed = append(reversed, asc.Rows[i].Row.ID)
	}
	if equalIDs(ids(desc), reversed) {
		t.Fatalf("desc ids %v equal reversed asc ids %v: looks like a flip", ids(desc), reversed)
	}

	// Sanity: the multiset of (rank, dense) values must match
	// between asc and desc; only which row holds them differs.
	counts := map[[2]int]int{}
	for _, r := range asc.Rows {
		counts[[2]int{r.Rank, r.DenseRank}]++
	}
	for _, r := range desc.Rows {
		k := [2]int{r.Rank, r.DenseRank}
		counts[k]--
		if counts[k] < 0 {
			t.Errorf("desc has extra (rank,dense)=%v not in asc multiset", k)
		}
	}
	for k, c := range counts {
		if c != 0 {
			t.Errorf("(rank,dense)=%v multiset mismatch: net %d", k, c)
		}
	}
}

// TestDescendingAllTied keeps RANK=DENSE_RANK=1 for all rows and
// orders RowNumber by ID ascending even in desc mode.
func TestDescendingAllTied(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 9, ID: 3},
		{Partition: strPtr("p"), Value: 9, ID: 1},
		{Partition: strPtr("p"), Value: 9, ID: 2},
	}
	res := Compute(rows, Options{Desc: true})
	wantIDs := []int64{1, 2, 3}
	if got := ids(res); !equalIDs(got, wantIDs) {
		t.Fatalf("desc all-tied: got ids %v, want %v", got, wantIDs)
	}
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
	})
}
