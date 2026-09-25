package ranking

import (
	"fmt"
	"math/rand"
	"testing"
)

// ceilLog2 返回 ceil(log2(n))，n >= 1。
func ceilLog2(n int) int {
	ceil := 0
	for (1 << ceil) < n {
		ceil++
	}
	return ceil
}

// 对 n 行的一个分区，排序比较次数不得超过 10*n*ceil(log2(n+1))，
// 防止实现退化成两两比较的 O(n^2)。
func TestComparisonCountBound(t *testing.T) {
	for _, n := range []int{1, 2, 7, 64, 255, 1000, 4096} {
		rows := make([]Row, n)
		rng := rand.New(rand.NewSource(int64(n)))
		for i := range rows {
			rows[i] = Row{
				Partition: strPtr("p"),
				Value:     float64(rng.Intn(n/2 + 1)), // 制造大量并列
				ID:        fmt.Sprintf("id%06d", rng.Int()),
			}
		}
		var count int64
		opts := Options{Compare: func(a, b Row) int {
			count++
			return defaultCompare(a, b)
		}}
		Rank(rows, opts)
		bound := int64(10 * n * ceilLog2(n+1))
		if count > bound {
			t.Errorf("n=%d: comparisons = %d, bound = %d", n, count, bound)
		}
	}
}

// 降序路径同样受比较次数上界约束。
func TestComparisonCountBoundDescending(t *testing.T) {
	const n = 2048
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{Partition: strPtr("p"), Value: float64(i % 16), ID: itoa(i)}
	}
	var count int64
	opts := Options{
		Descending: true,
		Compare: func(a, b Row) int {
			count++
			return descendingCompare(a, b)
		},
	}
	Rank(rows, opts)
	if bound := int64(10 * n * ceilLog2(n+1)); count > bound {
		t.Errorf("n=%d: comparisons = %d, bound = %d", n, count, bound)
	}
}
