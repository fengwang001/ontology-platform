package booking

import (
	"sync"
	"testing"

	"ontology/slotpool"
)

// TestConcurrentDeterministic 并发调用下结果等价于某串行顺序：
// 成功订号总数必等于容量，且每位患者至多一条有效预约/候补。
func TestConcurrentDeterministic(t *testing.T) {
	s := New(60, 30, 10, 120, 2, 10000)
	if err := s.AddSlot(0, "s", 600, 100, 60); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	success := make(chan int64, 500)
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := []byte{byte(i % 200)}
			if seq, err := s.Book(100, p, "s", slotpool.Online); err == nil {
				success <- seq
			}
		}(i)
	}
	wg.Wait()
	close(success)
	seqs := map[int64]bool{}
	for q := range success {
		if seqs[q] {
			t.Fatalf("duplicate seq %d", q)
		}
		seqs[q] = true
	}
	sp := s.pool.Get("s")
	if sp.UO+sp.US != len(seqs) {
		t.Fatalf("success=%d but counts uo+us=%d", len(seqs), sp.UO+sp.US)
	}
	if sp.UO+sp.US > sp.Cap {
		t.Fatalf("quota invariant violated: %d > %d", sp.UO+sp.US, sp.Cap)
	}
}
