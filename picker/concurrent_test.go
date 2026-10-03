package picker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentInvariants 并发混合调用 Pick/Release/Add/Remove，
// 全程用只读访问器校验跨包不变量；配合 -race 检查数据竞争。
func TestConcurrentInvariants(t *testing.T) {
	cfg := Config{Tau: 1000, Prior: 50, FailPenalty: 1000, MaxInflight: 4, MaxEndpoints: 8}
	p := newTestPicker(t, cfg)
	mustAdd(t, p, "a", "b", "c", "d")

	var clock int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 记账 goroutine：成功 Pick 的票据登记，Release 后移除。
	var mu sync.Mutex
	live := map[int64]bool{}

	// 不变量巡检：Σ inflight == 未归还票据数；每端点 ≤ M。
	checkerDone := make(chan struct{})
	go func() {
		defer close(checkerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			now := atomic.LoadInt64(&clock)
			stats, openCount := p.Snapshot(now)
			var sum int64
			for _, st := range stats {
				if st.Inflight < 0 || st.Inflight > cfg.MaxInflight {
					t.Errorf("endpoint %s inflight=%d out of [0,%d]", st.ID, st.Inflight, cfg.MaxInflight)
					return
				}
				if st.Draining {
					// 排空端点必须满足 infl≥1（归零即被移除）。
					if st.Inflight == 0 {
						t.Errorf("draining endpoint %s should have been removed at zero", st.ID)
						return
					}
				}
				sum += st.Inflight
			}
			if sum != int64(openCount) {
				t.Errorf("invariant violated: sum inflight=%d != open tickets=%d", sum, openCount)
				return
			}
		}
	}()

	// Pick worker。
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			r1 := seed
			for i := 0; i < 3000; i++ {
				now := atomic.AddInt64(&clock, 1)
				r1 = r1*6364136223846793005 + 1442695040888963407
				r2 := r1*2862933555777941757 + 3037000493
				tk, _, err := p.Pick(now, r1, r2)
				if err == nil {
					mu.Lock()
					live[tk] = true
					mu.Unlock()
				}
			}
		}(uint64(w + 1))
	}

	// Release worker：消耗已记录的票据。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4000; i++ {
				mu.Lock()
				var tk int64
				for cand := range live {
					tk = cand
					break
				}
				mu.Unlock()
				if tk == 0 {
					continue
				}
				// 多个 worker 可能取到同一票据；时钟戳顺序与锁内顺序也可能
				// 颠倒。只有成功归还才移除票据；其余错误下票据保持有效，稍后重试。
				for attempt := 0; attempt < 8; attempt++ {
					now := atomic.AddInt64(&clock, 1)
					err := p.Release(tk, int64(now%500), now%7 != 0, now)
					if err == nil {
						mu.Lock()
						delete(live, tk)
						mu.Unlock()
						break
					}
					if errors.Is(err, ErrTicket) {
						mu.Lock()
						delete(live, tk) // 已被其他 worker 归还。
						mu.Unlock()
						break
					}
					if errors.Is(err, ErrClockBackward) {
						continue // 用更大的 now 重试。
					}
					t.Errorf("unexpected release err: %v", err)
					return
				}
			}
		}()
	}

	// 生命周期 worker：摘除/重建已空闲的端点集合（使用独立 id 避免与主集合票据竞争）。
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(base string) {
			defer wg.Done()
			id := base
			for i := 0; i < 500; i++ {
				_ = p.AddEndpoint(id)
				_ = p.RemoveEndpoint(id)
			}
		}(string(rune('x' + w)))
	}

	wg.Wait()
	close(stop)
	<-checkerDone

	// 结束后所有在途票据仍可归还，账本最终自洽。
	mu.Lock()
	final := make([]int64, 0, len(live))
	for tk := range live {
		final = append(final, tk)
	}
	mu.Unlock()
	now := atomic.LoadInt64(&clock)
	for _, tk := range final {
		now++
		if err := p.Release(tk, 20, true, now); err != nil {
			t.Fatalf("final release %d: %v", tk, err)
		}
	}
	if p.OpenTickets() != 0 {
		t.Fatalf("residual open tickets=%d", p.OpenTickets())
	}
	var sum int64
	for _, id := range p.EndpointIDs() {
		infl, _ := p.Inflight(id)
		sum += infl
	}
	if sum != 0 {
		t.Fatalf("residual inflight sum=%d", sum)
	}
}
