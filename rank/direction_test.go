package rank

import "testing"

func row(p, id string, v float64) Row {
	return Row{Partition: strptr(p), ID: id, SortValue: v}
}

// Descending: ranks follow value order 30,20,20,10, tie ranks unchanged,
// and the two 20 rows stay ID-ascending (not reversed).
func TestDescending(t *testing.T) {
	rows := []Row{
		row("p", "ten", 10),
		row("p", "twentyA", 20),
		row("p", "twentyB", 20),
		row("p", "thirty", 30),
	}
	res := Rank(rows, Desc)
	want := []struct {
		id   string
		tri  [3]int
	}{
		{"thirty", [3]int{1, 1, 1}},
		{"twentyA", [3]int{2, 2, 2}},
		{"twentyB", [3]int{3, 2, 2}},
		{"ten", [3]int{4, 4, 3}},
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("got %d rows", len(res.Rows))
	}
	for i, w := range want {
		if res.Rows[i].ID != w.id {
			t.Fatalf("row %d: id=%s want %s", i, res.Rows[i].ID, w.id)
		}
		assertTriple(t, res.Rows[i], w.tri[0], w.tri[1], w.tri[2])
	}
}

// Descending output must not be the ascending output reversed: doing so
// would also reverse the tie-internal ID order.
func TestDescendingNotReverseOfAscending(t *testing.T) {
	rows := []Row{
		row("p", "a", 10),
		row("p", "b", 20),
		row("p", "c", 20),
		row("p", "d", 30),
	}
	asc := Rank(rows, Asc).Rows
	desc := Rank(rows, Desc).Rows

	n := len(asc)
	for i := 0; i < n; i++ {
		reversed := asc[n-1-i]
		if reversed.ID == desc[i].ID &&
			reversed.RowNumber == desc[i].RowNumber {
			t.Fatalf("desc row %d is the exact reverse of asc row %d (%s)",
				i, n-1-i, desc[i].ID)
		}
	}

	// Positive check: tie group 20 keeps b before c under descending.
	if desc[1].ID != "b" || desc[2].ID != "c" {
		t.Fatalf("tie internal order must stay ID-ascending: %s, %s",
			desc[1].ID, desc[2].ID)
	}
	// Sanity: values themselves are in descending order.
	for i := 1; i < n; i++ {
		if desc[i].SortValue > desc[i-1].SortValue {
			t.Fatalf("desc values not descending at %d", i)
		}
	}
}
