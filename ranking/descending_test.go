package ranking

import "testing"

// 降序：RANK/DENSE_RANK 并列规则不变，并列内部仍按行 ID 升序。
func TestRankDescending(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 10, ID: "a"},
		{Partition: strPtr("p"), Value: 20, ID: "b"},
		{Partition: strPtr("p"), Value: 20, ID: "c"},
		{Partition: strPtr("p"), Value: 30, ID: "d"},
	}
	got := Rank(rows, Options{Descending: true})
	assertTriples(t, got, []string{"d", "b", "c", "a"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

// 降序结果不得是升序结果的简单倒置：
// 倒置升序会让并列内部行 ID 变成降序，这里断言它不是。
func TestDescendingIsNotReversedAscending(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 20, ID: "x"},
		{Partition: strPtr("p"), Value: 10, ID: "y"},
		{Partition: strPtr("p"), Value: 20, ID: "z"},
	}
	asc := Rank(rows, Options{})
	desc := Rank(rows, Options{Descending: true})
	n := len(asc.Rows)
	reversed := make([]RankedRow, n)
	for i, r := range asc.Rows {
		reversed[n-1-i] = r
	}
	sameAsReversal := true
	for i := range desc.Rows {
		if desc.Rows[i].Row.ID != reversed[i].Row.ID {
			sameAsReversal = false
			break
		}
	}
	if sameAsReversal {
		t.Fatal("descending output equals simple reversal of ascending output")
	}
	// 降序下并列内部仍按行 ID 升序（x 在 z 前），而非降序。
	if desc.Rows[0].Row.ID != "x" || desc.Rows[1].Row.ID != "z" {
		t.Errorf("tie order = %s,%s; want x,z (row ID ascending)",
			desc.Rows[0].Row.ID, desc.Rows[1].Row.ID)
	}
	// 且降序的 ROW_NUMBER 在并列处仍从 1 顺排。
	assertTriples(t, desc, []string{"x", "z", "y"}, []triple{
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
	})
}
