package upload

import (
	"fmt"
	"sync"
	"testing"
)

// 语义 8：并发 Put 的守恒性与 race 干净。
func TestConcurrentPutConservation(t *testing.T) {
	const total = 16
	const workers = 8
	const rounds = 50
	u := New(total, 1)

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	lastSize := make(map[int]int64)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				n := (w*rounds+i)%total + 1
				size := int64((w+1)*1000 + i)
				if err := u.Put(n, size, fmt.Sprintf("w%d-i%d", w, i)); err != nil {
					t.Errorf("Put(%d) = %v", n, err)
					return
				}
				mu.Lock()
				successes++
				lastSize[n] = size
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	r := u.Stat()
	if r.Received != total {
		t.Fatalf("Received = %d, want %d", r.Received, total)
	}
	var wantBytes int64
	for _, s := range lastSize {
		wantBytes += s
	}
	if r.Bytes != wantBytes {
		t.Fatalf("Bytes = %d, want %d", r.Bytes, wantBytes)
	}
	if r.Replaced != successes-r.Received {
		t.Fatalf("Replaced = %d, want %d", r.Replaced, successes-r.Received)
	}
}
