package sched_test

import (
	"sync"
	"testing"

	"ontology/sched"
)

// TestConcurrent：多 goroutine 并发调用全部入口，在 -race 下验证
// "结果等价于某个串行顺序"。
func TestConcurrent(t *testing.T) {
	s := sched.New(3, 100, 2, 1000)
	if err := s.Register("d", 20, 0, 10, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64((g*200 + i) % 400)
				_ = s.Enqueue("d", "x", 1, 0, 500, now)
				_, _ = s.Deliver("d", now)
				_ = s.Ack("d", "x", now)
			}
		}(g)
	}
	wg.Wait()
}
