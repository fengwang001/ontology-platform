package rmq

import (
	"math/rand"
	"testing"
)

// TestQueryVisitsLogarithmic 证明 Query 访问节点数随 n 对数增长，
// 是线段树而非逐元素扫描（扫描是 n 量级）。
func TestQueryVisitsLogarithmic(t *testing.T) {
	ceilLog2 := func(n int) int {
		k := 0
		for s := 1; s < n; s <<= 1 {
			k++
		}
		return k
	}
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		arr := make([]int64, n)
		rng := rand.New(rand.NewSource(int64(n)))
		for i := range arr {
			arr[i] = rng.Int63n(1 << 40)
		}
		r := Build(arr)
		bound := int64(4*ceilLog2(n) + 8)

		r.Query(0, n) // 全区间
		if v := r.lastVisits.Load(); v > bound {
			t.Fatalf("n=%d Query(0,n) 访问 %d 节点，超对数界 %d", n, v, bound)
		}
		for k := 0; k < 50; k++ { // 随机小区间
			l := rng.Intn(n)
			w := 1 + rng.Intn(8)
			rr := l + w
			if rr > n {
				rr = n
			}
			r.Query(l, rr)
			if v := r.lastVisits.Load(); v > bound {
				t.Fatalf("n=%d Query(%d,%d) 访问 %d 节点，超对数界 %d", n, l, rr, v, bound)
			}
		}
		if n == 10000 && r.lastVisits.Load() >= int64(n) {
			t.Fatalf("n=%d 访问量接近 n，疑似逐元素扫描", n)
		}
	}
}
