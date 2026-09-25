package ranking

import "testing"

// 分区隔离：每个分区的三种排名各自从 1 开始。
func TestPartitionRanksRestartAtOne(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("a"), Value: 1, ID: "a1"},
		{Partition: strPtr("a"), Value: 2, ID: "a2"},
		{Partition: strPtr("b"), Value: 100, ID: "b1"},
		{Partition: strPtr("b"), Value: 100, ID: "b2"},
		{Partition: strPtr("b"), Value: 200, ID: "b3"},
	}
	got := Rank(rows, Options{})
	assertTriples(t, got, []string{"a1", "a2", "b1", "b2", "b3"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
	})
}

// 分区之间的输出顺序按分区键字典序；空串是合法分区且排在最前。
func TestPartitionOrderLexicographic(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("z"), Value: 1, ID: "z1"},
		{Partition: strPtr(""), Value: 1, ID: "e1"},
		{Partition: strPtr("ab"), Value: 1, ID: "ab1"},
		{Partition: strPtr("aa"), Value: 1, ID: "aa1"},
	}
	got := Rank(rows, Options{})
	wantIDs := []string{"e1", "aa1", "ab1", "z1"}
	wantParts := []string{"", "aa", "ab", "z"}
	if len(got.Rows) != len(wantIDs) || got.Skipped != 0 {
		t.Fatalf("got %d rows, %d skipped; want %d rows, 0 skipped",
			len(got.Rows), got.Skipped, len(wantIDs))
	}
	for i := range wantIDs {
		r := got.Rows[i]
		if r.Row.ID != wantIDs[i] || *r.Row.Partition != wantParts[i] {
			t.Errorf("row %d: got (%s, part=%q), want (%s, part=%q)",
				i, r.Row.ID, *r.Row.Partition, wantIDs[i], wantParts[i])
		}
		if r.RowNumber != 1 || r.Rank != 1 || r.DenseRank != 1 {
			t.Errorf("row %d: ranks = (%d,%d,%d), want (1,1,1)",
				i, r.RowNumber, r.Rank, r.DenseRank)
		}
	}
}

// nil 分区键的行必须被拒绝并计入跳过计数，其余行不受影响。
func TestNilPartitionSkipped(t *testing.T) {
	rows := []Row{
		{Partition: nil, Value: 1, ID: "bad1"},
		{Partition: strPtr("p"), Value: 1, ID: "ok1"},
		{Partition: nil, Value: 2, ID: "bad2"},
		{Partition: strPtr("p"), Value: 2, ID: "ok2"},
	}
	got := Rank(rows, Options{})
	if got.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", got.Skipped)
	}
	assertTriples(t, got, []string{"ok1", "ok2"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
	})
}
