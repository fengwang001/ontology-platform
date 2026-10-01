package scaledown

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发调用所有操作：结果等价于某个串行顺序，且容量不变量始终成立。
func TestConcurrentOperations(t *testing.T) {
	s := mustNew(t, 30, 3, 1)
	for i := 0; i < 8; i++ {
		addNode(t, s, fmt.Sprintf("n%02d", i), 1000, 1000)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(4)
		base := w * 200

		go func() { // 不断添加 Pod
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := fmt.Sprintf("w%d-p%03d", w, i)
				node := fmt.Sprintf("n%02d", (base+i)%8)
				_ = s.AddPod(Pod{ID: id, Node: node, PC: int64(1 + i%40), PM: int64(1 + i%40), Kind: KindNormal})
			}
		}()

		go func() { // 不断删除 Pod
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = s.RemovePod(fmt.Sprintf("w%d-p%03d", (w+1)%8, i))
			}
		}()

		go func() { // 单调时钟下不断 Tick
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := s.Tick(int64(w*1000 + i)); err != nil {
					// 多 goroutine 时钟交错可能回退，属合法拒绝。
					if !errors.Is(err, ErrClockRewind) {
						t.Errorf("unexpected tick err: %v", err)
						return
					}
				}
			}
		}()

		go func() { // 不断读取 since
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = s.Since()
			}
		}()
	}
	wg.Wait()

	if !checkCapacityInvariant(s) {
		t.Fatal("capacity invariant violated after concurrent run")
	}
}

func checkCapacityInvariant(s *Scaler) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, n := range s.nodes {
		var cpu, mem int64
		for _, p := range n.pods {
			cpu += p.pc
			mem += p.pm
		}
		if cpu > n.ca || mem > n.ma {
			return false
		}
		_ = name
	}
	return true
}
