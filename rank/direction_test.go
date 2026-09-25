package rank

import (
	"math"
	"reflect"
	"testing"
)

func TestDescNotReverseOfAsc(t *testing.T) {
	rows := makeRows("p", []float64{10, 20, 20, 30})
	asc := RankRows(rows, Asc).Rankings
	desc := RankRows(rows, Desc).Rankings

	assertTriples(t, &Result{Rankings: desc}, []triple{
		{1, 1, 1}, // 30
		{2, 2, 2}, // 20, ID 较小者
		{3, 2, 2}, // 20
		{4, 4, 3}, // 10
	})

	// 值序列确实是降序。
	for i := 1; i < len(desc); i++ {
		if desc[i].Value > desc[i-1].Value {
			t.Fatalf("降序结果在第 %d 行未按值降序排列", i)
		}
	}

	// 降序不是升序的简单倒置：倒置后并列行内部顺序会相反，
	// 而要求是两种方向下并列行都按 ID 升序。
	reversed := make([]Ranking, len(asc))
	for i := range asc {
		reversed[i] = asc[len(asc)-1-i]
	}
	if reflect.DeepEqual(desc, reversed) {
		t.Fatalf("降序结果恰好是升序结果的倒置：%v", desc)
	}
	// 具体断言：值 20 的两个并列位置，两种方向下 ID 顺序相同（均为升序）。
	if desc[1].ID != asc[1].ID || desc[2].ID != asc[2].ID {
		t.Fatalf("并列行内部未按 ID 升序: desc=%s,%s asc=%s,%s",
			desc[1].ID, desc[2].ID, asc[1].ID, asc[2].ID)
	}
}

func TestDescAllTies(t *testing.T) {
	res := RankRows(makeRows("p", []float64{5, 5, 5, 5}), Desc)
	assertTriples(t, res, []triple{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

func TestInfinitiesOrderedAtEnds(t *testing.T) {
	rows := []Row{
		{Partition: ptr("p"), Value: math.Inf(1), ID: "top"},
		{Partition: ptr("p"), Value: 1, ID: "mid"},
		{Partition: ptr("p"), Value: math.Inf(-1), ID: "bot"},
	}
	asc := RankRows(rows, Asc).Rankings
	if asc[0].ID != "bot" || asc[1].ID != "mid" || asc[2].ID != "top" {
		t.Fatalf("升序下 ±Inf 位置错误: %s,%s,%s", asc[0].ID, asc[1].ID, asc[2].ID)
	}
	desc := RankRows(rows, Desc).Rankings
	if desc[0].ID != "top" || desc[1].ID != "mid" || desc[2].ID != "bot" {
		t.Fatalf("降序下 ±Inf 位置错误: %s,%s,%s", desc[0].ID, desc[1].ID, desc[2].ID)
	}
}
