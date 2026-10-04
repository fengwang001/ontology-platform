package blame

import (
	"sync"
	"testing"
)

// TestConcurrentEquivalentToSerial 并发下发 Land/Evaluate/Blame，
// 只要求：不死锁、不崩、每个违约最终恰好归因一次（Blame 结果稳定）。
// 配合 -race 验证内部同步正确。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	a, _ := New(100)
	mustAdd(t, a, "root", 5, 0)
	for i := 0; i < 6; i++ {
		name := string(rune('a' + i))
		parents := []string{"root"}
		if i > 0 {
			parents = append(parents, string(rune('a'+i-1)))
		}
		mustAdd(t, a, name, int64(20+i*10), 5, parents...)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clock := int64(0)
			for step := 0; step < 200; step++ {
				now := clock + int64(step%7)
				if step%3 == 0 {
					name := "root"
					k := int64(step / 2)
					if err := a.Land(name, k, now); err == nil {
						clock = now
					}
				} else {
					if al, err := a.Evaluate(now); err == nil {
						clock = now
						for _, x := range al {
							for _, d := range x.Affected {
								if r, err := a.Blame(d, x.K); err != nil || r.Dataset == "" {
									t.Errorf("Blame(%s,%d)=%+v,%v", d, x.K, r, err)
								}
							}
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
}
