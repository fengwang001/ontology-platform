package swiss

import (
	"errors"
	"sync"
	"testing"
)

// 本轮尚有未登记的盘时，并发 Pair 全部得到 ErrPending。
func TestConcurrentPairWhilePending(t *testing.T) {
	tr, _ := New(3)
	mustRegister(t, tr, "p1", "p2", "p3", "p4")
	if _, _, err := tr.Pair(); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := tr.Pair(); !errors.Is(err, ErrPending) {
				t.Errorf("concurrent pending Pair: %v", err)
			}
		}()
	}
	wg.Wait()
}
