package ontology

import "testing"

func rowIn(key string, v float64, id string) Row {
	return Row{Partition: &key, Value: v, ID: id}
}

func TestPartitionIsolationAndOrder(t *testing.T) {
	rows := []Row{
		rowIn("z", 1, "z1"),
		rowIn("", 9, "e1"),
		rowIn("a", 5, "a1"),
		rowIn("a", 1, "a2"),
		rowIn("", 1, "e2"),
	}
	got := Rank(rows, Asc).Rows

	wantKeys := []string{"", "", "a", "a", "z"}
	for i, key := range wantKeys {
		if got[i].Partition != key {
			t.Fatalf("row %d partition = %q, want %q", i, got[i].Partition, key)
		}
		if got[i].RowNumber != 1 && got[i].RowNumber != 2 {
			t.Fatalf("row numbering leaked across partitions: %d", got[i].RowNumber)
		}
	}
	if got[0].Rank != 1 || got[2].Rank != 1 {
		t.Fatalf("ranks must restart at 1 per partition: %#v", got)
	}
}

func TestNilPartitionAndNaNAreSkipped(t *testing.T) {
	key := "p"
	rows := []Row{
		{Partition: &key, Value: 1, ID: "ok1"},
		{Partition: nil, Value: 2, ID: "nil"},
		{Partition: &key, Value: 3, ID: "ok2"},
		{Partition: &key, Value: nan(), ID: "nan"},
		{Partition: nil, Value: nan(), ID: "both"},
	}
	rep := Rank(rows, Asc)
	if rep.Skipped != 3 {
		t.Fatalf("Skipped = %d, want 3", rep.Skipped)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("accepted rows = %d, want 2", len(rep.Rows))
	}
	for _, r := range rep.Rows {
		if r.ID == "nil" || r.ID == "nan" || r.ID == "both" {
			t.Fatalf("skipped row leaked into output: %s", r.ID)
		}
	}
}

func TestSignedZeroTieAndInfinities(t *testing.T) {
	rows := mkRows([]float64{
		inf(1), 5, negZero(), posZero(), inf(-1),
	}, "r")
	got := Rank(rows, Asc).Rows
	order := []float64{inf(-1), negZero(), posZero(), 5, inf(1)}
	if got[0].Value != order[0] || got[4].Value != order[4] {
		t.Fatalf("infinities must bracket the values: %#v", got)
	}
	if got[1].Rank != 2 || got[2].Rank != 2 {
		t.Fatalf("+0.0/-0.0 must tie, got ranks %d %d", got[1].Rank, got[2].Rank)
	}
	if got[1].ID != "r3" || got[2].ID != "r4" {
		t.Fatalf("tie order by id = %s,%s want r3,r4", got[1].ID, got[2].ID)
	}
}
