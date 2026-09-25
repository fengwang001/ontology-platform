package ranking

import (
	"fmt"
	"math/rand"
	"testing"
)

// 输入不被修改：行切片与每个行结构在调用后逐字段不变；
// 返回的结果切片是新分配的。
func TestInputNotMutated(t *testing.T) {
	k0, k1 := "b", "a"
	rows := []Row{
		{PartitionKey: &k0, SortValue: 3, ID: "x"},
		{PartitionKey: &k1, SortValue: 1, ID: "y"},
		{PartitionKey: &k0, SortValue: 2, ID: "z"},
	}
	backup := make([]Row, len(rows))
	copy(backup, rows)

	got := Rank(rows, Options{Descending: true})

	for i := range rows {
		if rows[i] != backup[i] {
			t.Errorf("第 %d 行被修改：%+v -> %+v", i, backup[i], rows[i])
		}
	}
	if k0 != "b" || k1 != "a" {
		t.Error("分区键指向的字符串被修改")
	}
	if len(got.Rows) == 0 {
		t.Fatal("结果为空")
	}
	// 修改返回结果不得影响再次调用的输出（结果独立分配）。
	got.Rows[0].RowNumber = -999
	again := Rank(rows, Options{Descending: true})
	if again.Rows[0].RowNumber == -999 {
		t.Error("返回的结果切片不是独立分配的")
	}
}

// 比较次数上界：n 行单分区的排序比较次数不超过 10*n*ceil(log2(n+1))。
func TestComparisonCountBound(t *testing.T) {
	for _, n := range []int{1, 2, 7, 64, 1000, 4096} {
		rng := rand.New(rand.NewSource(int64(n)))
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				PartitionKey: StringPtr("p"),
				SortValue:    rng.Float64() * 100,
				ID:           fmt.Sprintf("r%05d", i),
			}
		}
		count := 0
		opts := Options{Compare: func(a, b float64) int {
			count++
			return defaultCompare(a, b)
		}}
		Rank(rows, opts)

		log2 := 0
		for x := n + 1; x > 0; x >>= 1 {
			log2++
		} // log2 == ceil(log2(n+1))
		bound := 10 * n * log2
		if count > bound {
			t.Errorf("n=%d：比较次数 %d 超过上界 %d", n, count, bound)
		}
	}
}
