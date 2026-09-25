package stat

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
)

func TestInvariantsUnderRandomLoad(t *testing.T) {
	rec := &Recorder{}
	br, err := breaker.New(breaker.Config{
		ConsecutiveFailures: 10, MinSamples: 50, FailureRate: 0.35,
		Cooldown: time.Millisecond, MaxCooldown: 4 * time.Millisecond, HalfOpenProbes: 2,
	})
	if err != nil {
		t.Fatalf("breaker.New: %v", err)
	}
	bh, err := bulkhead.New(bulkhead.Config{Concurrency: 6, Queue: 2})
	if err != nil {
		t.Fatalf("bulkhead.New: %v", err)
	}
	ex := NewExecutor(br, bh, time.Millisecond, rec)

	const total = 50000
	seeds := rand.New(rand.NewSource(42))
	var counter atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				if counter.Add(1) > total {
					return
				}
				var fn func(context.Context) error
				switch n := rng.Intn(100); {
				case n < 45:
					fn = func(context.Context) error { return nil }
				case n < 70:
					fn = func(context.Context) error { return errors.New("retryable") }
				case n < 75:
					fn = func(context.Context) error { return classify.MarkNonRetryable(errors.New("bad req")) }
				case n < 85:
					fn = func(context.Context) error { time.Sleep(time.Millisecond); return nil }
				case n < 95:
					fn = func(context.Context) error { time.Sleep(10 * time.Millisecond); return nil }
				default:
					fn = func(context.Context) error { panic("boom") }
				}
				_ = ex.Do(context.Background(), fn)
			}
		}(seeds.Int63())
	}
	wg.Wait()

	s := rec.Snapshot()
	if v := s.Violations(); len(v) > 0 {
		t.Fatalf("口径不自洽: %v", v)
	}
	t.Logf("total=%d real=%d breakerRej=%d bulkheadRej=%d success=%d failed=%d byKind=%v",
		s.Total, s.Real, s.BreakerReject, s.BulkheadReject, s.Success, s.Failed, s.ByKind)
	for name, n := range map[string]int64{
		"breakerRej": s.BreakerReject, "bulkheadRej": s.BulkheadReject,
		"timeout失败": s.ByKind[classify.Timeout], "panic失败": s.ByKind[classify.Retryable],
	} {
		if n == 0 {
			t.Errorf("%s 为 0，随机序列未覆盖该口径", name)
		}
	}
}

func TestRejectedCallsNotCounted(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	br, err := breaker.New(breaker.Config{
		ConsecutiveFailures: 3, MinSamples: 10, FailureRate: 0.5,
		Cooldown: time.Second, MaxCooldown: time.Second, HalfOpenProbes: 1,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("breaker.New: %v", err)
	}
	bh, err := bulkhead.New(bulkhead.Config{Concurrency: 4, Queue: 4})
	if err != nil {
		t.Fatalf("bulkhead.New: %v", err)
	}
	rec := &Recorder{}
	ex := NewExecutor(br, bh, 0, rec)
	fail := func(context.Context) error { return errors.New("down") }
	ok := func(context.Context) error { return nil }

	for i := 0; i < 3; i++ { // 真实失败，打开熔断
		_ = ex.Do(context.Background(), fail)
	}
	before := rec.Snapshot()
	peakBefore := bh.Peak()
	for i := 0; i < 1000; i++ { // 打开后连续 1000 次被拒
		_ = ex.Do(context.Background(), ok)
	}
	after := rec.Snapshot()
	if after.BreakerReject-before.BreakerReject != 1000 {
		t.Fatalf("breakerRej 增量 = %d, want 1000", after.BreakerReject-before.BreakerReject)
	}
	if after.Real != before.Real || after.Failed != before.Failed {
		t.Fatalf("被拒调用污染了真实调用/失败统计: %+v -> %+v", before, after)
	}
	if bh.Peak() != peakBefore || bh.InFlight() != 0 { // 熔断在前：被拒调用不占用舱壁
		t.Fatalf("熔断打开期间舱壁被占用: peak %d->%d inflight=%d", peakBefore, bh.Peak(), bh.InFlight())
	}
	now = now.Add(time.Second) // 冷却结束，半开一次成功即关闭
	if err := ex.Do(context.Background(), ok); err != nil {
		t.Fatalf("半开探测 err = %v", err)
	}
	if br.State() != breaker.Closed {
		t.Fatalf("state = %v, want closed", br.State())
	}
	if v := rec.Snapshot().Violations(); len(v) > 0 {
		t.Fatalf("口径不自洽: %v", v)
	}
}
