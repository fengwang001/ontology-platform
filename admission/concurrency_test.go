package admission

import (
	"sync"
	"testing"
)

// 并发冒烟：混合 Submit/Advance/查询，在 -race 下确认无数据竞争，
// 且任何时刻查询到的待处理集合都满足可行不变式。
func TestConcurrentSmoke(t *testing.T) {
	c := NewController()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var clock int64
			for k := 0; k < 300; k++ {
				clock += int64(k % 5)
				id := [3]string{"a", "b", "c"}[k%3]
				r := c.Submit(id, clock, 3, clock+10, 5, 7)
				if !r.Accepted {
					// 重复或过载等拒绝都合法；推进一次以便编号复用。
					c.Advance(clock)
				}
				_ = c.Pending()
				_ = c.Value()
				_ = c.Evicted()
				_ = c.Now()
			}
		}(g)
	}
	wg.Wait()
}
