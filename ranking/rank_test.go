package ranking

import "testing"

// strPtr 返回字符串指针，测试辅助。
func strPtr(s string) *string { return &s }

// makeRows 用同一分区键和给定排序值构造行，行 ID 为 "r0","r1",...
func makeRows(part string, values ...float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: strPtr(part), Value: v, ID: "r" + itoa(i)}
	}
	return rows
}

// itoa 是小整数转字符串的辅助，避免引入 strconv 的格式化开销。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// triple 是一行的三种排名期望值。
type triple struct {
	rowNumber, rank, denseRank int
}

// assertTriples 逐行断言三种排名，并断言行 ID 次序。
// 跳过计数由各测试自行断言。
func assertTriples(t *testing.T, got Result, ids []string, want []triple) {
	t.Helper()
	if len(got.Rows) != len(want) {
		t.Fatalf("len(Rows) = %d, want %d", len(got.Rows), len(want))
	}
	for i, w := range want {
		r := got.Rows[i]
		if r.Row.ID != ids[i] {
			t.Errorf("row %d: ID = %q, want %q", i, r.Row.ID, ids[i])
		}
		if r.RowNumber != w.rowNumber || r.Rank != w.rank || r.DenseRank != w.denseRank {
			t.Errorf("row %d (%s): got (%d,%d,%d), want (%d,%d,%d)",
				i, r.Row.ID, r.RowNumber, r.Rank, r.DenseRank,
				w.rowNumber, w.rank, w.denseRank)
		}
	}
}

// [10,20,20,30] 升序：ROW_NUMBER 1,2,3,4；RANK 1,2,2,4；DENSE_RANK 1,2,2,3。
func TestRankClassicTie(t *testing.T) {
	got := Rank(makeRows("p", 10, 20, 20, 30), Options{})
	assertTriples(t, got, []string{"r0", "r1", "r2", "r3"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

// [5,5,5,5] 全并列：ROW_NUMBER 1,2,3,4；RANK 全 1；DENSE_RANK 全 1。
func TestRankAllTied(t *testing.T) {
	got := Rank(makeRows("p", 5, 5, 5, 5), Options{})
	assertTriples(t, got, []string{"r0", "r1", "r2", "r3"}, []triple{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

// [1,2,2,2,3] 中间多重并列：RANK 1,2,2,2,5；DENSE_RANK 1,2,2,2,3。
func TestRankMiddleTies(t *testing.T) {
	got := Rank(makeRows("p", 1, 2, 2, 2, 3), Options{})
	assertTriples(t, got, []string{"r0", "r1", "r2", "r3", "r4"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}
