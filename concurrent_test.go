package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 并发 Add 不得丢元素、不得让同一元素出现两次；
// 并发 Add 与 Sample 混合执行不得触发数据竞争（用 -race 验证）。
func TestConcurrentAddNoLossNoDup(t *testing.T) {
	const workers = 8
	const perWorker = 500
	s, err := NewSampler(64, 99)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				item := fmt.Sprintf("w%d-%d", w, i)
				if err := s.Add(item, float64(i%3+1)); err != nil {
					t.Error(err)
					return
				}
				if i%50 == 0 {
					_ = s.Sample()
					_ = s.Size()
				}
			}
		}(w)
	}
	wg.Wait()
	if s.Total() != workers*perWorker {
		t.Fatalf("Total()=%d, want %d", s.Total(), workers*perWorker)
	}
	if s.Size() > 64 {
		t.Fatalf("Size()=%d exceeds k=64", s.Size())
	}
	seen := make(map[string]int)
	for _, it := range s.Sample() {
		seen[it.(string)]++
	}
	for item, n := range seen {
		if n != 1 {
			t.Fatalf("item %s appears %d times in sample", item, n)
		}
	}
}
