package lc

import (
	"math/rand"
	"testing"
)

// TestPickChecksBounded 证明 Pick 用最小堆定位而非整表扫描：
// 连接数互异的 m 台服务器，一次 Pick 检查的服务器个数不随 m 线性增长。
func TestPickChecksBounded(t *testing.T) {
	const bound = 4 // 与 m 无关的小常数（堆顶 peek 实际为 1）
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := New(m)
		for i := 0; i < m; i++ { // 造互异连接数：0,1,...,m-1
			for j := 0; j < i; j++ {
				c.Incr(i)
			}
		}
		want := 0 // 连接数 0 的是下标 0
		if got := c.Pick(); got != want {
			t.Fatalf("m=%d: Pick()=%d, want %d", m, got, want)
		}
		if c.checks > bound {
			t.Fatalf("m=%d: Pick checked %d servers, exceeds constant bound %d", m, c.checks, bound)
		}
	}
}

// TestPickMatchesNaive 随机操作序列下，堆的 Pick 与朴素 O(n) 扫描逐一一致。
func TestPickMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 3, 17, 100} {
		rng := rand.New(rand.NewSource(int64(n)))
		c := New(n)
		ref := make([]int, n)
		for step := 0; step < 2000; step++ {
			i := rng.Intn(n)
			if rng.Intn(2) == 0 {
				c.Incr(i)
				ref[i]++
			} else if ref[i] > 0 {
				c.Decr(i)
				ref[i]--
			}
			want := 0 // 朴素扫描：最少且下标最小
			for j := 1; j < n; j++ {
				if ref[j] < ref[want] {
					want = j
				}
			}
			if got := c.Pick(); got != want {
				t.Fatalf("n=%d step=%d: Pick()=%d, naive=%d", n, step, got, want)
			}
			for j := 0; j < n; j++ {
				if c.Count(j) != ref[j] || ref[j] < 0 {
					t.Fatalf("n=%d step=%d: Count(%d)=%d, ref=%d", n, step, j, c.Count(j), ref[j])
				}
			}
		}
	}
}
