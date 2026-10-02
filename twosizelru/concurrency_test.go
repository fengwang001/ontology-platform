package twosizelru_test

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	manager := newManager(t, 8, 50, 1, 10)
	for page := 0; page < 6; page++ {
		mustAccess(t, manager, page, int64(page))
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := manager.Access(1, 20); err != nil {
				t.Errorf("Access: %v", err)
			}
			if err := manager.Pin(2); err != nil {
				t.Errorf("Pin: %v", err)
			}
			if err := manager.Unpin(2); err != nil {
				t.Errorf("Unpin: %v", err)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := manager.Prefetch(7, 20); err != nil {
				t.Errorf("Prefetch: %v", err)
			}
			_, _ = manager.Lists()
		}()
	}

	wg.Wait()
	young, old := manager.Lists()
	if len(young)+len(old) != 7 {
		t.Fatalf("pool size = %d, want 7", len(young)+len(old))
	}

	seen := make(map[int]bool)
	for _, page := range append(append([]int(nil), young...), old...) {
		if seen[page] {
			t.Fatalf("page %d appears twice in Y%v O%v", page, young, old)
		}
		seen[page] = true
	}
}
