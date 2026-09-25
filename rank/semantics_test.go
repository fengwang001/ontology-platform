package rank

import "testing"

// [10,20,20,30] ascending: the three tie semantics must be distinguishable
// on the same rows at the same time.
func TestSemanticsTiesSkip(t *testing.T) {
	res := Rank(makeRows(10, 20, 20, 30), Asc)
	want := []struct{ rn, rk, dr int }{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(res.Rows), len(want))
	}
	for i, w := range want {
		assertTriple(t, res.Rows[i], w.rn, w.rk, w.dr)
	}
}

// [5,5,5,5]: ROW_NUMBER still enumerates, both rank columns stay at 1.
func TestSemanticsAllTied(t *testing.T) {
	res := Rank(makeRows(5, 5, 5, 5), Asc)
	for i, row := range res.Rows {
		assertTriple(t, row, i+1, 1, 1)
	}
}

// [1,2,2,2,3]: a multi-row tie in the middle.
func TestSemanticsMiddleTie(t *testing.T) {
	res := Rank(makeRows(1, 2, 2, 2, 3), Asc)
	want := []struct{ rn, rk, dr int }{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	}
	for i, w := range want {
		assertTriple(t, res.Rows[i], w.rn, w.rk, w.dr)
	}
}
