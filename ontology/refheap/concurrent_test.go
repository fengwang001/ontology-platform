package refheap

import (
	"sync"
	"testing"
)

// Hammer the heap concurrently: linearizable operations must always leave the
// used-bytes accounting consistent with the surviving object sizes, and no
// reference may ever dangle at an observed quiescent point.
func TestConcurrentHammer(t *testing.T) {
	h, _ := NewHeap(500, 5, 3)
	q := h.CreateQueue()

	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			now := int64(seed)
			for i := 0; i < 400; i++ {
				now++
				switch i % 9 {
				case 0:
					if id, err := h.Alloc(1+int64(i%30), i%3, i%2 == 0); err == nil && id%3 == 0 {
						_ = h.SetRoot(id)
					}
				case 1:
					_, _ = h.NewRef(RefKind(1+seed%3), 0, q, 1+int64(i%5), now)
				case 2:
					_, _ = h.Collect(now)
				case 3:
					_, _ = h.Poll(q)
				case 4:
					_, _ = h.Finalize(i % 4)
				case 5:
					if id, err := h.Alloc(5, 1, false); err == nil {
						_ = h.ClearRoot(id)
					}
				case 6:
					// monotonic Get (timestamp seed differs per worker, so
					// expect some clock rejections; that is fine).
					_, _ = h.Get(1, now)
				case 7:
					_, _ = h.Alloc(1, 0, true)
				default:
					_, _ = h.Collect(now)
				}
			}
		}(w)
	}
	wg.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	var sum int64
	for _, ob := range h.objs {
		sum += ob.size
		if ob.isRef && ob.target != 0 {
			if _, ok := h.objs[ob.target]; !ok {
				t.Fatalf("dangling target %d", ob.target)
			}
		}
	}
	if sum != h.used || h.used > h.capacity {
		t.Fatalf("accounting sum=%d used=%d cap=%d", sum, h.used, h.capacity)
	}
}
