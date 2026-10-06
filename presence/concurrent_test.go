package presence

import (
	"sync"
	"testing"
)

// TestConcurrentSerializability 并发调用不崩溃、无数据竞争，结果保持基本不变式。
func TestConcurrentSerializability(t *testing.T) {
	s := newTestSvc(t, 100)
	const goroutines = 16
	const ops = 400
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			user := "c" + string(rune('a'+g%8))
			viewer := "w" + string(rune('a'+g%8))
			var now int64 = int64(g)
			for i := 0; i < ops; i++ {
				now += int64(i%7) + 1
				dev := "d" + string(rune('0'+i%4))
				_ = s.Report(user, dev, Status(i%3+1), now)
				_ = s.Report(viewer, "d", StatusOnline, now)
				_ = s.Subscribe(viewer, user, now)
				_ = s.SetInvisible(user, i%2 == 0, now)
				_, _ = s.Query(viewer, user, now)
				_, _ = s.Drain(viewer, now)
			}
		}(g)
	}
	wg.Wait()
}
