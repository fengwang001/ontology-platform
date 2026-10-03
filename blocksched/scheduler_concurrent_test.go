package blocksched_test

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/blocksched"
)

// TestConcurrentSafe 在 -race 下验证所有方法可并发调用，
// 且调度始终满足内部不变量。
func TestConcurrentSafe(t *testing.T) {
	cfg := blocksched.Config{
		Blocks: 4, BaseConcurrent: 3, GlobalLimit: 8,
		MaxBlockDup: 3, Timeout: 7, BanThreshold: 3,
	}
	sched := blocksched.New(cfg)
	var wg sync.WaitGroup
	var clock int64

	// 时钟由所有 goroutine 共享并单调推进：拿到旧读数的调用只会收到 ErrClock，
	// 等价于某个合法串行顺序中一次被拒绝的调用。
	now := func() int64 {
		v := atomic.LoadInt64(&clock)
		if rand.Intn(3) == 0 {
			atomic.AddInt64(&clock, int64(rand.Intn(5)))
			v = atomic.LoadInt64(&clock)
		}
		return v
	}

	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		id := id
		have := make([]bool, cfg.Blocks)
		for b := range have {
			have[b] = rand.Intn(2) == 0
		}
		_ = sched.AddPeer(id, have)
	}

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				id := ids[rand.Intn(len(ids))]
				n := now()
				switch rand.Intn(6) {
				case 0:
					_, _, _ = sched.Next(n, id)
				case 1:
					b, ok, err := sched.Next(n, id)
					if ok && err == nil {
						_, _, _ = sched.Done(n, id, b, rand.Intn(2) == 0)
					}
				case 2:
					_ = sched.Have(id, rand.Intn(cfg.Blocks))
				case 3:
					_, _ = sched.Tick(n)
				case 4:
					_, _, _ = sched.Done(n, id, rand.Intn(cfg.Blocks), rand.Intn(2) == 0)
				case 5:
					_ = sched.Complete()
				}
				if err := sched.DebugInvariants(); err != nil {
					t.Errorf("invariant: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
