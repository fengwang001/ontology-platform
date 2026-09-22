package ttlcache

import (
	"fmt"
	"testing"
)

// 装满 N 项且全部同时过期时，单次驱逐考察的候选数不随 N 线性增长。
func TestEvictExaminedSublinear(t *testing.T) {
	examined := func(n int) int {
		c, clk := newCache(t, n)
		for i := 0; i < n; i++ {
			mustPut(t, c, fmt.Sprintf("k%d", i), "v", 10) // 同一时刻写入
		}
		clk.Advance(10) // 全部同时过期
		mustPut(t, c, "trigger", "v", 100)
		return c.lastEvictExamined
	}

	e100 := examined(100)
	e1000 := examined(1000)
	t.Logf("N=100 考察 %d 项, N=1000 考察 %d 项", e100, e1000)

	if e100 < 1 || e1000 < 1 {
		t.Fatal("驱逐应至少考察 1 个候选")
	}
	// 树路径长度量级为 log2(N)：100→7, 1000→10，留充足余量仍远小于 N。
	if limit := 64; e100 > limit || e1000 > limit {
		t.Errorf("考察数应远小于 N: N=100 → %d, N=1000 → %d", e100, e1000)
	}
	if e1000 > e100*2+16 {
		t.Errorf("考察数随 N 线性增长: N=100 → %d, N=1000 → %d", e100, e1000)
	}
}
