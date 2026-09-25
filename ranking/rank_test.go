package ranking

import "testing"

// makeRows 用同一分区构造一批行，ID 为 "r0".."rn-1"。
func makeRows(values []float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		id := "r" + string(rune('0'+i))
		rows[i] = Row{PartitionKey: StringPtr("p"), SortValue: v, ID: id}
	}
	return rows
}

// assertRanks 逐行断言三列排名与行 ID 次序。
func assertRanks(t *testing.T, got []RankedRow, ids []string, rn, rk, dr []int) {
	t.Helper()
	if len(got) != len(ids) {
		t.Fatalf("行数 = %d，期望 %d", len(got), len(ids))
	}
	for i, g := range got {
		if g.Row.ID != ids[i] {
			t.Errorf("第 %d 行 ID = %q，期望 %q", i, g.Row.ID, ids[i])
		}
		if g.RowNumber != rn[i] || g.Rank != rk[i] || g.DenseRank != dr[i] {
			t.Errorf("第 %d 行 (ID=%s) = (%d,%d,%d)，期望 (%d,%d,%d)",
				i, g.Row.ID, g.RowNumber, g.Rank, g.DenseRank, rn[i], rk[i], dr[i])
		}
	}
}

// [10,20,20,30] 升序：ROW_NUMBER 1,2,3,4；RANK 1,2,2,4；DENSE_RANK 1,2,2,3。
func TestRankClassicTie(t *testing.T) {
	got := Rank(makeRows([]float64{10, 20, 20, 30}), Options{})
	assertRanks(t, got.Rows,
		[]string{"r0", "r1", "r2", "r3"},
		[]int{1, 2, 3, 4},
		[]int{1, 2, 2, 4},
		[]int{1, 2, 2, 3})
	if got.SkippedNilPartition != 0 || got.SkippedNaN != 0 {
		t.Errorf("跳过计数 = (%d,%d)，期望 (0,0)", got.SkippedNilPartition, got.SkippedNaN)
	}
}

// [5,5,5,5] 全并列：ROW_NUMBER 1,2,3,4；RANK 全 1；DENSE_RANK 全 1。
func TestRankAllTied(t *testing.T) {
	got := Rank(makeRows([]float64{5, 5, 5, 5}), Options{})
	assertRanks(t, got.Rows,
		[]string{"r0", "r1", "r2", "r3"},
		[]int{1, 2, 3, 4},
		[]int{1, 1, 1, 1},
		[]int{1, 1, 1, 1})
}

// [1,2,2,2,3] 中间多重并列：RANK 1,2,2,2,5；DENSE_RANK 1,2,2,2,3。
func TestRankMultiTie(t *testing.T) {
	got := Rank(makeRows([]float64{1, 2, 2, 2, 3}), Options{})
	assertRanks(t, got.Rows,
		[]string{"r0", "r1", "r2", "r3", "r4"},
		[]int{1, 2, 3, 4, 5},
		[]int{1, 2, 2, 2, 5},
		[]int{1, 2, 2, 2, 3})
}

// 并列内部 ROW_NUMBER 由行 ID 升序决定，而非输入下标。
func TestTieBreakByRowID(t *testing.T) {
	rows := []Row{
		{PartitionKey: StringPtr("p"), SortValue: 7, ID: "c"},
		{PartitionKey: StringPtr("p"), SortValue: 7, ID: "a"},
		{PartitionKey: StringPtr("p"), SortValue: 7, ID: "b"},
	}
	got := Rank(rows, Options{})
	assertRanks(t, got.Rows,
		[]string{"a", "b", "c"},
		[]int{1, 2, 3},
		[]int{1, 1, 1},
		[]int{1, 1, 1})
}
