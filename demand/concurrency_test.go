package demand_test

import (
	"sync"
	"testing"

	"ontology/demand"
)

// 并发调用结果等价于某个串行顺序：高并发下不得数据竞争或状态损坏。
func TestConcurrentCalls(t *testing.T) {
	c, err := demand.New(baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		if err := c.AddLoad(0, mkLoad(i, 5, i, 0, 0)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tt := int64(1)
			for k := 0; k < 200; k++ {
				tt++
				_, _ = c.Report(tt, 60) // 大量会因时刻回退被拒，属正常
				_ = c.Peak()
				_ = c.LockLoad(tt, 1+g%8)
				_ = c.UnlockLoad(tt, 1+g%8)
			}
		}(g)
	}
	wg.Wait()
}
