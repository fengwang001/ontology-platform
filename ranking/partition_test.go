package ranking

import (
	"math"
	"testing"
)

// 分区隔离：每个分区排名各自从 1 开始；空串是合法分区；
// 分区之间输出按分区键字典序。
func TestPartitionIsolationAndOrder(t *testing.T) {
	rows := []Row{
		{PartitionKey: StringPtr("b"), SortValue: 100, ID: "b0"},
		{PartitionKey: StringPtr(""), SortValue: 2, ID: "e1"},
		{PartitionKey: StringPtr("a"), SortValue: 9, ID: "a1"},
		{PartitionKey: StringPtr("b"), SortValue: 50, ID: "b1"},
		{PartitionKey: StringPtr(""), SortValue: 1, ID: "e0"},
		{PartitionKey: StringPtr("a"), SortValue: 9, ID: "a0"},
	}
	got := Rank(rows, Options{})
	if len(got.Rows) != 6 {
		t.Fatalf("行数 = %d，期望 6", len(got.Rows))
	}

	// 字典序："" < "a" < "b"。
	wantPart := []string{"", "", "a", "a", "b", "b"}
	for i, r := range got.Rows {
		if *r.Row.PartitionKey != wantPart[i] {
			t.Errorf("第 %d 行分区 = %q，期望 %q", i, *r.Row.PartitionKey, wantPart[i])
		}
	}
	// 空串分区：e0(1) 在 e1(2) 前，各自从 1 起。
	assertRanks(t, got.Rows[0:2], []string{"e0", "e1"}, []int{1, 2}, []int{1, 2}, []int{1, 2})
	// 分区 a：并列 9，RANK 全 1、DENSE_RANK 全 1，ROW_NUMBER 按 ID。
	assertRanks(t, got.Rows[2:4], []string{"a0", "a1"}, []int{1, 2}, []int{1, 1}, []int{1, 1})
	// 分区 b：排名重新从 1 开始，与分区 a 的值域无关。
	assertRanks(t, got.Rows[4:6], []string{"b1", "b0"}, []int{1, 2}, []int{1, 2}, []int{1, 2})
}

// nil 分区键与 NaN 排序值的行被拒绝并计入跳过计数，不参与任何排名。
func TestSkippedRows(t *testing.T) {
	rows := []Row{
		{PartitionKey: StringPtr("p"), SortValue: 1, ID: "ok0"},
		{PartitionKey: nil, SortValue: 2, ID: "nilKey"},
		{PartitionKey: StringPtr("p"), SortValue: math.NaN(), ID: "nan"},
		{PartitionKey: nil, SortValue: math.NaN(), ID: "both"},
		{PartitionKey: StringPtr("p"), SortValue: 3, ID: "ok1"},
	}
	got := Rank(rows, Options{})
	if got.SkippedNilPartition != 2 {
		t.Errorf("SkippedNilPartition = %d，期望 2", got.SkippedNilPartition)
	}
	if got.SkippedNaN != 1 {
		t.Errorf("SkippedNaN = %d，期望 1（nil 键优先计入）", got.SkippedNaN)
	}
	assertRanks(t, got.Rows, []string{"ok0", "ok1"}, []int{1, 2}, []int{1, 2}, []int{1, 2})
}

// +0.0 与 -0.0 视为相等构成并列；±Inf 合法并排在两端。
func TestSpecialFloatValues(t *testing.T) {
	rows := []Row{
		{PartitionKey: StringPtr("p"), SortValue: math.Inf(+1), ID: "inf"},
		{PartitionKey: StringPtr("p"), SortValue: 0.0, ID: "pos0"},
		{PartitionKey: StringPtr("p"), SortValue: math.Inf(-1), ID: "ninf"},
		{PartitionKey: StringPtr("p"), SortValue: math.Copysign(0, -1), ID: "neg0"},
	}
	got := Rank(rows, Options{})
	assertRanks(t, got.Rows,
		[]string{"ninf", "neg0", "pos0", "inf"},
		[]int{1, 2, 3, 4},
		[]int{1, 2, 2, 4},
		[]int{1, 2, 2, 3})
	if got.SkippedNaN != 0 {
		t.Errorf("SkippedNaN = %d，期望 0", got.SkippedNaN)
	}
}
