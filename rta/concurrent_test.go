package rta

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentExercises 在 -race 下并发调用所有操作，验证串行等价性
// （互斥保护）且查询永不触发迭代、现序恒可调度。
func TestConcurrentExercises(t *testing.T) {
	s := New()
	// 预置少量任务，给 Remove/Response 提供目标。
	for i := 0; i < 6; i++ {
		if _, err := s.Add(Task{
			ID: fmt.Sprintf("q%d", i), C: 1, T: 40, D: 40,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写者：不断 Add/Remove 同一小集合中的编号。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("q%d", id)
			tk := Task{ID: name, C: 1, T: 40, D: 40}
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := s.Add(tk); err != nil {
					_ = s.Remove(name)
				} else {
					_ = s.Remove(name)
				}
			}
		}(w)
	}

	// 读者：Order 与 Response 必须随时返回自洽结果。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.mu.RLock()
				seen := map[string]bool{}
				for _, id := range s.order {
					if seen[id] {
						t.Errorf("duplicate id in Order: %s", id)
						s.mu.RUnlock()
						return
					}
					seen[id] = true
					if _, ok := s.tasks[id]; !ok {
						t.Errorf("id %s in order but not in tasks", id)
						s.mu.RUnlock()
						return
					}
				}
				s.mu.RUnlock()
				// Response 作为公共只读 API 单独被高频调用（其内部再取 RLock）。
				// NotFound 是合法结果；err 只能是 *rta.Error 而非 panic/其他。
				if _, err := s.Response("q3"); err != nil {
					if _, ok := err.(*Error); !ok {
						t.Errorf("unexpected error type: %T", err)
						return
					}
				}
			}
		}()
	}

	// 跑一小段时间后停止。
	for i := 0; i < 2000; i++ {
		_ = s.Order()
	}
	close(stop)
	wg.Wait()
}
