package vegas

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAcquireRelease 验证并发调用等价于某个合法串行顺序。
//
// 时间安排采用阶段（phase）屏障：每个阶段分配一个严格递增的 now，
// 阶段内的多个 Acquire/Release 由不同 goroutine 同时起跑、真正并发执行
// （同阶段 now 相同），阶段结束后才进入下一阶段。因此被接受的调用
// 构成一个非降 now 的合法串行序，不应产生时钟回退错误；同阶段内的
// 放行/归还竞争又由实现内部的互斥锁裁决。
//
// Lmin=Lmax=20 使 cut 与 Vegas 调整都被夹到 20，L 恒定，便于精确断言：
// 在途峰值不超过 L、同一令牌恰好发放/归还一次、结束后 n=0。
func TestConcurrentAcquireRelease(t *testing.T) {
	l := mustNew(t, Config{L0: 20, Lmin: 20, Lmax: 20, Alpha: 2, Beta: 4,
		Tmo: 1_000_000_000, Cooldown: 1000, Wm: 1_000_000_000})

	const (
		parallel = 64
		phases   = 400
	)

	var granted, returned, peak int64
	var peakMu sync.Mutex
	seen := make(map[int64]struct{})
	var seenMu sync.Mutex
	var failErr atomic.Value
	setFail := func(err error) { failErr.CompareAndSwap(nil, err) }

	type task struct {
		tk Token // 零值表示 Acquire；非零表示 Release 该令牌
	}
	grantCh := make(chan Token, parallel)

	runPhase := func(now int64, pt []task) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, j := range pt {
			wg.Add(1)
			go func(j task) {
				defer wg.Done()
				<-start
				if j.tk.Seq == 0 {
					tk, err := l.Acquire(now)
					if err == ErrAtCapacity {
						return
					}
					if err != nil {
						setFail(err)
						return
					}
					atomic.AddInt64(&granted, 1)
					seenMu.Lock()
					if _, dup := seen[tk.Seq]; dup {
						seenMu.Unlock()
						setFail(ErrInvalidToken)
						return
					}
					seen[tk.Seq] = struct{}{}
					seenMu.Unlock()
					cur := l.N()
					peakMu.Lock()
					if cur > peak {
						peak = cur
					}
					peakMu.Unlock()
					if cur > l.L() {
						setFail(ErrAtCapacity)
						return
					}
					grantCh <- tk
				} else {
					if err := l.Release(j.tk.Seq, Ignored, 0, now); err != nil {
						setFail(err)
						return
					}
					atomic.AddInt64(&returned, 1)
				}
			}(j)
		}
		close(start)
		wg.Wait()
	}

	drainGrants := func(pool []Token) []Token {
		for {
			select {
			case tk := <-grantCh:
				pool = append(pool, tk)
			default:
				return pool
			}
		}
	}

	var pool []Token
	now := int64(0)
	for p := 0; p < phases; p++ {
		pool = drainGrants(pool)
		var pt []task
		for i := 0; i < parallel; i++ {
			if len(pool) > 0 && i%2 == 0 {
				pt = append(pt, task{tk: pool[0]})
				pool = pool[1:]
			} else {
				pt = append(pt, task{})
			}
		}
		now += 2
		runPhase(now, pt)
		if e := failErr.Load(); e != nil {
			t.Fatalf("phase %d worker error: %v", p, e)
		}
	}
	pool = drainGrants(pool)

	// 以严格递增的 now 归还剩余令牌。
	for _, tk := range pool {
		now += 2
		runPhase(now, []task{{tk: tk}})
	}
	if e := failErr.Load(); e != nil {
		t.Fatalf("drain worker error: %v", e)
	}

	if l.N() != 0 {
		t.Fatalf("drained n=%d", l.N())
	}
	if l.L() != 20 {
		t.Fatalf("L=%d want 20", l.L())
	}
	if peak > 20 {
		t.Fatalf("peak in flight %d > L=20", peak)
	}
	if g, r := atomic.LoadInt64(&granted), atomic.LoadInt64(&returned); g == 0 || g != r {
		t.Fatalf("granted=%d returned=%d", g, r)
	}
	if int64(len(seen)) != atomic.LoadInt64(&granted) {
		t.Fatalf("unique=%d granted=%d", len(seen), atomic.LoadInt64(&granted))
	}
	t.Logf("granted=%d peak=%d", granted, peak)
}
