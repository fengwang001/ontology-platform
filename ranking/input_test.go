package ranking

import "testing"

// snapshot 逐字段复制行切片，用于调用前后比对。
func snapshot(rows []Row) []Row {
	cp := make([]Row, len(rows))
	copy(cp, rows)
	return cp
}

// 传入的行切片与每个行结构在调用后必须逐字段不变。
func TestInputNotMutated(t *testing.T) {
	pa, pb := "a", "b"
	rows := []Row{
		{Partition: &pb, Value: 20, ID: "r2"},
		{Partition: &pa, Value: 10, ID: "r1"},
		{Partition: &pb, Value: 20, ID: "r3"},
		{Partition: &pa, Value: 5, ID: "r0"},
	}
	before := snapshot(rows)
	Rank(rows, Options{})
	Rank(rows, Options{Descending: true})
	if len(rows) != len(before) {
		t.Fatalf("len(rows) = %d, want %d", len(rows), len(before))
	}
	for i := range rows {
		got, want := rows[i], before[i]
		if got.Partition != want.Partition || got.Value != want.Value || got.ID != want.ID {
			t.Errorf("row %d mutated: got %+v, want %+v", i, got, want)
		}
	}
}

// 返回的结果切片是新分配的：改写结果不影响输入，再次调用结果互不影响。
func TestResultIsFreshlyAllocated(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 2, ID: "b"},
		{Partition: strPtr("p"), Value: 1, ID: "a"},
	}
	first := Rank(rows, Options{})
	if len(first.Rows) == 0 {
		t.Fatal("empty result")
	}
	// 改写返回结果，不应影响输入行。
	first.Rows[0].Row.Value = 999
	first.Rows[0].Row.ID = "corrupted"
	if rows[0].Value != 2 || rows[0].ID != "b" || rows[1].Value != 1 || rows[1].ID != "a" {
		t.Errorf("input rows changed via result: %+v", rows)
	}
	// 再次调用，结果不受上次改写影响。
	second := Rank(rows, Options{})
	if second.Rows[0].Row.ID != "a" || second.Rows[1].Row.ID != "b" {
		t.Errorf("second call affected by mutation: %s,%s",
			second.Rows[0].Row.ID, second.Rows[1].Row.ID)
	}
}
