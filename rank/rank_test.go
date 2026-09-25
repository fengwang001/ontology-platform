package rank

import (
	"math"
	"testing"
)

func ptr(s string) *string { return &s }

func makeRows(part string, vals []float64) []Row {
	rows := make([]Row, len(vals))
	for i, v := range vals {
		rows[i] = Row{Partition: &part, Value: v, ID: idFor(i)}
	}
	return rows
}

func idFor(i int) string {
	// 保证 ID 升序与生成顺序一致且为两位数宽度，避免 "10" < "2" 之类的字典序问题。
	return string(rune('a' + i%26))
}

type triple struct{ rn, rk, dr int }

func assertTriples(t *testing.T, res *Result, want []triple) {
	t.Helper()
	if len(res.Rankings) != len(want) {
		t.Fatalf("行数 = %d, 期望 %d", len(res.Rankings), len(want))
	}
	for i, w := range want {
		got := res.Rankings[i]
		if got.RowNumber != w.rn || got.Rank != w.rk || got.DenseRank != w.dr {
			t.Fatalf("第 %d 行: (ROW_NUMBER,RANK,DENSE_RANK)=(%d,%d,%d), 期望 (%d,%d,%d)",
				i, got.RowNumber, got.Rank, got.DenseRank, w.rn, w.rk, w.dr)
		}
	}
}

func TestTenTwentyTwentyThirty(t *testing.T) {
	res := RankRows(makeRows("p", []float64{10, 20, 20, 30}), Asc)
	assertTriples(t, res, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

func TestAllTies(t *testing.T) {
	res := RankRows(makeRows("p", []float64{5, 5, 5, 5}), Asc)
	assertTriples(t, res, []triple{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

func TestMiddleTies(t *testing.T) {
	res := RankRows(makeRows("p", []float64{1, 2, 2, 2, 3}), Asc)
	assertTriples(t, res, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}

func TestSignedZeroTies(t *testing.T) {
	rows := []Row{
		{Partition: ptr("p"), Value: 0, ID: "z"},
		{Partition: ptr("p"), Value: math.Copysign(0, -1), ID: "a"},
	}
	res := RankRows(rows, Asc)
	if len(res.Rankings) != 2 {
		t.Fatalf("行数 = %d, 期望 2", len(res.Rankings))
	}
	// +0/-0 并列，组内按 ID 升序。
	if res.Rankings[0].ID != "a" || res.Rankings[0].Rank != 1 || res.Rankings[0].DenseRank != 1 {
		t.Fatalf("第一行异常: %+v", res.Rankings[0])
	}
	if res.Rankings[1].ID != "z" || res.Rankings[1].RowNumber != 2 || res.Rankings[1].Rank != 1 {
		t.Fatalf("第二行异常: %+v", res.Rankings[1])
	}
}
