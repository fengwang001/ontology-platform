package rbuf

import (
	"fmt"
	"testing"

	"ontology/spill"
)

// TestSpillSelectSublinear 证明溢写对象按堆定位：访问数 ≤ 8·⌈log2 m⌉+8，不随 m 线性增长。
func TestSpillSelectSublinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		// memLimit 足够大：m 个事务各持不同行数，最后一次 Append 才触发溢写。
		limit := 2*m + 1
		b := New(limit, spill.New(m+1))
		for tx := 1; tx <= m; tx++ {
			if err := b.Begin(tx); err != nil {
				t.Fatal(err)
			}
			for r := 0; r < tx%3+1; r++ { // 各事务行数不同（1~3）
				if err := b.Append(tx, fmt.Sprintf("t%d-%d", tx, r)); err != nil {
					t.Fatal(err)
				}
			}
		}
		for b.M() < limit { // 把 M 顶到 limit，下一次 Append 必溢写
			if err := b.Append(m, "pad"); err != nil {
				t.Fatal(err)
			}
		}
		if err := b.Append(1, "trigger"); err != nil {
			t.Fatal(err)
		}
		log2 := 0
		for x := m - 1; x > 0; x >>= 1 { // ⌈log2 m⌉
			log2++
		}
		if max := 8*log2 + 8; b.visited > max {
			t.Errorf("m=%d: visited=%d 超过上界 %d", m, b.visited, max)
		}
	}
}
