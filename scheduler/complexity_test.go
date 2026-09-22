package scheduler

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestTouchIndependentOfN(t *testing.T) {
	type result struct{ slots, timers int64 }
	run := func(n int) result {
		s := newSched(t, Config{MaxTimers: 200000})
		for i := 0; i < n; i++ {
			if _, err := s.Add(1000000, nil); err != nil { // 全部远在未来
				t.Fatal(err)
			}
		}
		if err := s.Advance(1); err != nil {
			t.Fatal(err)
		}
		return result{s.touchedSlots, s.touchedTimers}
	}
	small, big := run(1000), run(100000)
	t.Logf("N=1000: slots=%d timers=%d; N=100000: slots=%d timers=%d",
		small.slots, small.timers, big.slots, big.timers)
	if big.slots > small.slots*2+8 || big.timers > small.timers*2+8 {
		t.Fatalf("touched grows with N: %+v -> %+v", small, big)
	}
}

func TestConcurrent(t *testing.T) {
	s := newSched(t, Config{MaxTimers: 1 << 20})
	const workers, perWorker = 8, 500
	var added, cancelled, fired atomic.Int64
	var firedIDs, cancelledIDs sync.Map
	var stop atomic.Bool
	var advWg, workerWg sync.WaitGroup

	advWg.Add(1)
	go func() { // 推进者
		defer advWg.Done()
		for !stop.Load() {
			if err := s.Advance(1); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	for w := 0; w < workers; w++ {
		workerWg.Add(1)
		go func(w int) {
			defer workerWg.Done()
			for j := 0; j < perWorker; j++ {
				id := w*perWorker + j
				tm, err := s.Add(int64(j%20), func() {
					fired.Add(1)
					firedIDs.Store(id, true)
				})
				if err != nil {
					t.Error(err)
					return
				}
				added.Add(1)
				if j%2 == 0 {
					if err := s.Cancel(tm); err == nil {
						cancelled.Add(1)
						cancelledIDs.Store(id, true)
					}
				}
			}
		}(w)
	}
	workerWg.Wait() // 注册方全部结束后停掉推进者
	stop.Store(true)
	advWg.Wait()

	for s.Pending() > 0 { // 排空剩余定时器
		if err := s.Advance(1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	disjoint := true
	cancelledIDs.Range(func(k, _ any) bool {
		if _, ok := firedIDs.Load(k); ok {
			disjoint = false
			return false
		}
		return true
	})
	if !disjoint {
		t.Fatal("cancelled timer appeared in fired set")
	}
	if got := fired.Load() + cancelled.Load(); got != added.Load() {
		t.Fatalf("fired(%d)+cancelled(%d)=%d != added(%d)",
			fired.Load(), cancelled.Load(), got, added.Load())
	}
}
