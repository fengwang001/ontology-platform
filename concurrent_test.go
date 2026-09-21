package ontology

import (
	"sync"
	"testing"
)

// 并发 Add 不得丢置位：所有协程结束后，每个已插入元素必须立即可见。
func TestConcurrentAddNoLoss(t *testing.T) {
	const workers = 8
	const perWorker = 2000
	f, err := New(workers*perWorker, 0.001)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint64(w * perWorker)
			for i := uint64(0); i < perWorker; i++ {
				f.Add(elem("c", base+i))
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		base := uint64(w * perWorker)
		for i := uint64(0); i < perWorker; i++ {
			if !f.MayContain(elem("c", base+i)) {
				t.Fatalf("false negative after concurrent Add at %d", base+i)
			}
		}
	}
}

// 并发进行中：Bytes 必须始终是长度完整的快照，MayContain 不得 panic。
func TestConcurrentReadsDuringAdds(t *testing.T) {
	f, err := New(50000, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantLen := int((f.M() + 7) / 8)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := uint64(0); i < 50000; i++ {
			f.Add(elem("r", i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50000; i++ {
			if b := f.Bytes(); len(b) != wantLen {
				t.Errorf("Bytes snapshot length = %d, want %d", len(b), wantLen)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50000; i++ {
			f.MayContain(elem("r", uint64(i)))
		}
	}()
	wg.Wait()
}
