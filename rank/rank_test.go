package rank

import "testing"

func strptr(s string) *string { return &s }

// makeRows builds rows in a single partition "p" with IDs r01, r02, ...
// in the same order as values.
func makeRows(values ...float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		id := "r" + string(rune('a'+i)) // ra, rb, rc, ...
		rows[i] = Row{Partition: strptr("p"), Value: v, ID: id}
	}
	return rows
}

type want3 struct {
	rowNumber, rank, dense int
}

func assertColumns(t *testing.T, res Result, want []want3) {
	t.Helper()
	if res.Skipped != 0 {
		t.Fatalf("Skipped = %d, want 0", res.Skipped)
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("len(Rows) = %d, want %d", len(res.Rows), len(want))
	}
	for i, w := range want {
		got := res.Rows[i]
		if got.RowNumber != w.rowNumber || got.Rank != w.rank || got.DenseRank != w.dense {
			t.Errorf("row %d (id=%s value=%v): got (%d,%d,%d), want (%d,%d,%d)",
				i, got.Row.ID, got.Row.Value,
				got.RowNumber, got.Rank, got.DenseRank,
				w.rowNumber, w.rank, w.dense)
		}
	}
}

func TestSemantics_10_20_20_30(t *testing.T) {
	res := Rank(makeRows(10, 20, 20, 30), Config{})
	assertColumns(t, res, []want3{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

func TestSemantics_AllTied(t *testing.T) {
	res := Rank(makeRows(5, 5, 5, 5), Config{})
	assertColumns(t, res, []want3{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

func TestSemantics_MiddleTies(t *testing.T) {
	res := Rank(makeRows(1, 2, 2, 2, 3), Config{})
	assertColumns(t, res, []want3{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}

// Tied rows must be ordered by ascending row ID, and their ROW_NUMBER
// values must follow that order.
func TestTieOrderByRowID(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: 20, ID: "c"},
		{Partition: strptr("p"), Value: 20, ID: "a"},
		{Partition: strptr("p"), Value: 20, ID: "b"},
	}
	res := Rank(rows, Config{})
	wantIDs := []string{"a", "b", "c"}
	for i, id := range wantIDs {
		if res.Rows[i].Row.ID != id {
			t.Fatalf("row %d: id = %q, want %q", i, res.Rows[i].Row.ID, id)
		}
		if res.Rows[i].RowNumber != i+1 {
			t.Fatalf("row %d: RowNumber = %d, want %d", i, res.Rows[i].RowNumber, i+1)
		}
	}
}
