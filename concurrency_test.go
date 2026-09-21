package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentAddNoFalseNegatives 多协程并发 Add 后，所有
// 元素必须全部命中（不丢置位）。用 -race 运行时同时验证无
// 数据竞争。
func TestConcurrentAddNoFalseNegatives(t *testing.T) {
	const (
		goroutines = 8
		perG       = 2000
	)
	f, err := New(goroutines*perG, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				f.Add(insElem(base + i))
			}
		}(g * perG)
	}
	wg.Wait()
	for i := 0; i < goroutines*perG; i++ {
		if !f.MayContain(insElem(i)) {
			t.Fatalf("false negative for element %d after concurrent adds", i)
		}
	}
}

// TestConcurrentReadWriteVisibility 并发进行中：已 Add 完成的
// 元素立刻可见；Bytes 在并发下取到的必须是完整快照（每个 64 位
// 字要么是旧值要么是新值，绝不出现撕裂——由 -race 与最终状态
// 校验共同保证）。
func TestConcurrentReadWriteVisibility(t *testing.T) {
	const n = 8000
	f, err := New(n, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = f.Bytes()
				_ = f.EstimateCount()
				f.MayContain(insElem(0))
			}
		}()
	}
	for i := 0; i < n; i++ {
		f.Add(insElem(i))
		// Add 返回后元素必须立刻可见。
		if !f.MayContain(insElem(i)) {
			t.Fatalf("element %d not visible right after Add", i)
		}
	}
	close(stop)
	readers.Wait()
	for i := 0; i < n; i++ {
		if !f.MayContain(insElem(i)) {
			t.Fatalf("false negative for element %d", i)
		}
	}
}
