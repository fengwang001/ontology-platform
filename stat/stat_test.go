package stat_test

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/stat"
	"ontology/timeout"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// guard 按 DESIGN 顺序组合：熔断 → 舱壁 → 超时 → 真实调用 → 统计。
type guard struct {
	br *breaker.Breaker
	bh *bulkhead.Bulkhead
	st *stat.Stat
	d  time.Duration
}

func (g *guard) call(fn func() error) error {
	if err := g.br.Allow(); err != nil {
		g.st.AddRejectOpen() // ErrOpen 与 ErrClockRewind 都算熔断拒绝
		return err
	}
	rel, err := g.bh.Acquire(context.Background())
	if err != nil {
		g.st.AddRejectBulkhead()
		return err
	}
	defer rel()
	err = timeout.Do(g.d, fn)
	if err == nil {
		g.br.OnSuccess()
		g.st.AddSuccess()
		return nil
	}
	g.br.OnFailure()
	g.st.AddFailure(classify.Of(err))
	return err
}

func newGuard(clk *fakeClock, n, q int) *guard {
	br, _ := breaker.New(breaker.Config{
		ConsecutiveFailures: 5,
		MinSamples:          20,
		FailureRate:         0.5,
		Cooldown:            time.Second,
		MaxCooldown:         8 * time.Second,
		Probes:              1,
	}, clk)
	bh, _ := bulkhead.New(n, q)
	return &guard{br: br, bh: bh, st: &stat.Stat{}, d: 50 * time.Millisecond}
}

func TestRejectionsDoNotAffectFailureRate(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	g := newGuard(clk, 4, 4)
	for i := 0; i < 5; i++ { // 连续失败打开熔断
		_ = g.call(func() error { return errors.New("boom") })
	}
	if g.br.State() != breaker.Open {
		t.Fatal("breaker not open")
	}
	before := g.st.Snapshot()
	for i := 0; i < 1000; i++ {
		if err := g.call(func() error { return nil }); !errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("call %d: want ErrOpen", i)
		}
	}
	after := g.st.Snapshot()
	if after.FailureRate() != before.FailureRate() || after.Real != before.Real {
		t.Fatalf("rejection polluted stats: before=%+v after=%+v", before, after)
	}
	if after.RejectedOpen-before.RejectedOpen != 1000 {
		t.Fatalf("rejectedOpen delta=%d want 1000", after.RejectedOpen-before.RejectedOpen)
	}
	clk.advance(time.Second) // 冷却结束，半开一次成功即关闭
	if err := g.call(func() error { return nil }); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if g.br.State() != breaker.Closed {
		t.Fatalf("state=%v want closed after one success", g.br.State())
	}
}

func TestInvariantsAfterRandomOps(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	g := newGuard(clk, 4, 4)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 6250; i++ {
				switch r.Intn(8) {
				case 0, 1, 2, 3:
					_ = g.call(func() error { return nil })
				case 4:
					_ = g.call(func() error { return errors.New("retryable") })
				case 5:
					_ = g.call(func() error {
						return errors.Join(classify.ErrNonRetryable)
					})
				case 6:
					_ = g.call(func() error { return timeout.ErrTimeout })
				case 7:
					_ = g.call(func() error {
						time.Sleep(time.Millisecond) // 制造舱壁竞争
						if r.Intn(4) == 0 {
							panic("boom")
						}
						return nil
					})
				}
				if i%1000 == 0 {
					clk.advance(2 * time.Second) // 推动冷却进入半开
				}
			}
		}(int64(w))
	}
	wg.Wait()
	snap := g.st.Snapshot()
	if err := snap.Check(); err != nil {
		t.Fatalf("invariant violated: %v\nsnapshot=%+v", err, snap)
	}
	if snap.Total != 50000 {
		t.Fatalf("total=%d want 50000", snap.Total)
	}
	t.Logf("snapshot=%+v", snap)
}
