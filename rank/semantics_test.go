package rank

import "testing"

func strPtr(s string) *string { return &s }

// rowsFromValues builds a single-partition row set where row i
// gets value values[i] and ID int64(i+1).
func rowsFromValues(part string, values []float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: strPtr(part), Value: v, ID: int64(i + 1)}
	}
	return rows
}

// triples extracts (RowNumber, Rank, DenseRank) per result row.
func triples(res Result) [][3]int {
	out := make([][3]int, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = [3]int{r.RowNumber, r.Rank, r.DenseRank}
	}
	return out
}

func assertTriples(t *testing.T, res Result, want [][3]int) {
	t.Helper()
	got := triples(res)
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d (value %v, id %d): got (rn,rank,dense)=%v, want %v",
				i, res.Rows[i].Row.Value, res.Rows[i].Row.ID, got[i], want[i])
		}
	}
}

func TestSemanticsMixedTies(t *testing.T) {
	res := Compute(rowsFromValues("p", []float64{10, 20, 20, 30}), Options{})
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

func TestSemanticsAllTied(t *testing.T) {
	res := Compute(rowsFromValues("p", []float64{5, 5, 5, 5}), Options{})
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

func TestSemanticsMultiWayTie(t *testing.T) {
	res := Compute(rowsFromValues("p", []float64{1, 2, 2, 2, 3}), Options{})
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}

func TestSemanticsDistinctValues(t *testing.T) {
	res := Compute(rowsFromValues("p", []float64{3, 1, 2}), Options{})
	// Sorted by value: 1(id2), 2(id3), 3(id1).
	wantIDs := []int64{2, 3, 1}
	for i, id := range wantIDs {
		if res.Rows[i].Row.ID != id {
			t.Fatalf("row %d: got id %d, want %d", i, res.Rows[i].Row.ID, id)
		}
	}
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 3, 3},
	})
}
