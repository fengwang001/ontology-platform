package ranking

import (
	"math/rand"
	"testing"
)

// 同一批行打乱 20 种排列后，输出必须逐行完全相同（含 ROW_NUMBER）。
func TestDeterministicAcrossPermutations(t *testing.T) {
	base := []Row{
		{PartitionKey: StringPtr("b"), SortValue: 20, ID: "r3"},
		{PartitionKey: StringPtr("a"), SortValue: 20, ID: "r1"},
		{PartitionKey: StringPtr("a"), SortValue: 10, ID: "r0"},
		{PartitionKey: StringPtr("b"), SortValue: 20, ID: "r2"},
		{PartitionKey: StringPtr("a"), SortValue: 30, ID: "r4"},
		{PartitionKey: StringPtr("b"), SortValue: 5, ID: "r5"},
	}
	want := Rank(base, Options{})

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([]Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := Rank(shuffled, Options{})
		if got.SkippedNilPartition != want.SkippedNilPartition || got.SkippedNaN != want.SkippedNaN {
			t.Fatalf("第 %d 次排列：跳过计数不一致", trial)
		}
		if len(got.Rows) != len(want.Rows) {
			t.Fatalf("第 %d 次排列：行数 %d != %d", trial, len(got.Rows), len(want.Rows))
		}
		for i := range want.Rows {
			if got.Rows[i] != want.Rows[i] {
				t.Fatalf("第 %d 次排列：第 %d 行 %+v != %+v", trial, i, got.Rows[i], want.Rows[i])
			}
		}
	}
}

// 降序：RANK/DENSE_RANK 并列规则不变，并列内部仍按行 ID 升序，
// 且结果不是升序结果的简单倒置。
func TestDescendingNotReversedAscending(t *testing.T) {
	mk := func() []Row {
		return []Row{
			{PartitionKey: StringPtr("p"), SortValue: 10, ID: "d"},
			{PartitionKey: StringPtr("p"), SortValue: 20, ID: "b"},
			{PartitionKey: StringPtr("p"), SortValue: 20, ID: "a"},
			{PartitionKey: StringPtr("p"), SortValue: 30, ID: "c"},
		}
	}
	asc := Rank(mk(), Options{})
	desc := Rank(mk(), Options{Descending: true})

	// 降序期望：30(c) 第一；20 并列，内部按 ID 升序 a 在 b 前；10(d) 最后。
	assertRanks(t, desc.Rows,
		[]string{"c", "a", "b", "d"},
		[]int{1, 2, 3, 4},
		[]int{1, 2, 2, 4},
		[]int{1, 2, 2, 3})

	// 断言降序结果不是升序结果的简单倒置：倒置升序会把并列内部
	// 的 ID 次序也翻转（b 在 a 前），与正确降序结果不同。
	reversed := make([]RankedRow, len(asc.Rows))
	for i, r := range asc.Rows {
		reversed[len(asc.Rows)-1-i] = r
	}
	same := true
	for i := range reversed {
		if reversed[i].Row.ID != desc.Rows[i].Row.ID {
			same = false
			break
		}
	}
	if same {
		t.Error("降序结果与升序结果的简单倒置相同，疑似靠整体反转实现")
	}
}
