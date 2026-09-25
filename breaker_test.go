package ontology_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/classify"
)

type fakeClock struct{ t atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.t.Store(0)
	return c
}
func (c *fakeClock) Now() time.Time { return time.Unix(0, c.t.Load()) }
func (c *fakeClock) advance(d time.Duration) {
	for {
		old := c.t.Load()
		if c.t.CompareAndSwap(old, old+int64(d)) {
			return
		}
	}
}
func (c *fakeClock) rollback(d time.Duration) { c.t.Add(-int64(d)) }

func newTestBreaker(t *testing.T, c breaker.Clock) *breaker.Breaker {
	t.Helper()
	b, err := breaker.New(breaker.Config{
		Threshold: 3, MinSamples: 5, FailureRate: 0.5,
		Cooldown: 10 * time.Second, MaxCooldown: 40 * time.Second,
		Probes: 3, Clock: c,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBreakerTransitions(t *testing.T) {
	cases := []struct {
		name  string
		drive func(b *breaker.Breaker)
		want  breaker.State
		coolD time.Duration
		trans int
	}{
		{"closed stays", func(b *breaker.Breaker) {
			b.Record(nil)
		}, breaker.Closed, 10e9, 0},
		{"consecutive failures open", func(b *breaker.Breaker) {
			for i := 0; i < 3; i++ {
				b.Record(classify.ErrRetryable)
			}
		}, breaker.Open, 10e9, 1},
		{"rate over threshold opens", func(b *breaker.Breaker) {
			// 5 样本：3 失败 2 成功 => rate 0.6 > 0.5，连续失败仅 1。
			b.Record(classify.ErrRetryable)
			b.Record(nil)
			b.Record(nil)
			b.Record(classify.ErrTimeout)
			b.Record(classify.ErrRetryable)
		}, breaker.Open, 10e9, 1},
		{"fatal does not open", func(b *breaker.Breaker) {
			for i := 0; i < 10; i++ {
				b.Record(classify.ErrFatal)
			}
		}, breaker.Closed, 10e9, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := newFakeClock()
			b := newTestBreaker(t, clk)
			tc.drive(b)
			if got := b.State(); got != tc.want {
				t.Fatalf("state=%v want %v", got, tc.want)
			}
			if b.Cooldown() != tc.coolD {
				t.Fatalf("cooldown=%v want %v", b.Cooldown(), tc.coolD)
			}
			if b.Transitions() != tc.trans {
				t.Fatalf("transitions=%d want %d", b.Transitions(), tc.trans)
			}
		})
	}
}

func TestBreakerRecoveryAndDoubling(t *testing.T) {
	// 用例：打开→1000 次被拒不影响口径；冷却到半开；一次成功…
	// 探测数=3 时需 3 次全成功才回关闭；另构造 Probes=1 验证“一次成功即关闭”。
	clk := newFakeClock()
	b := newTestBreaker(t, clk)
	for i := 0; i < 3; i++ {
		b.Record(classify.ErrRetryable)
	}
	if b.RawState() != breaker.Open {
		t.Fatal("should be open")
	}
	rejected := 0
	for i := 0; i < 1000; i++ {
		if errors.Is(b.Allow(), breaker.ErrBreakerOpen) {
			rejected++
		}
	}
	if rejected != 1000 {
		t.Fatalf("rejected=%d", rejected)
	}
	clk.advance(10 * time.Second)
	if b.State() != breaker.HalfOpen {
		t.Fatal("should be half-open")
	}
	// 探测期任一失败 → 回打开，冷却翻倍。
	if err := b.Allow(); err != nil {
		t.Fatal(err)
	}
	b.Record(classify.ErrRetryable)
	if b.State() != breaker.Open || b.Cooldown() != 20*time.Second {
		t.Fatalf("state=%v cooldown=%v", b.State(), b.Cooldown())
	}
	clk.advance(20 * time.Second)
	if err := b.Allow(); err != nil {
		t.Fatal(err)
	}
	b.Record(classify.ErrRetryable)
	if b.Cooldown() != 40*time.Second {
		t.Fatalf("cooldown=%v want 40s", b.Cooldown())
	}
	// 再次翻倍被上限钳住。
	clk.advance(40 * time.Second)
	if err := b.Allow(); err != nil {
		t.Fatal(err)
	}
	b.Record(classify.ErrRetryable)
	if b.Cooldown() != 40*time.Second {
		t.Fatalf("cooldown=%v capped at 40s", b.Cooldown())
	}
	// 全部探测成功：回到关闭且冷却复位。
	clk.advance(40 * time.Second)
	for i := 0; i < 3; i++ {
		if err := b.Allow(); err != nil {
			t.Fatal(err)
		}
		b.Record(nil)
	}
	if b.RawState() != breaker.Closed || b.Cooldown() != 10*time.Second {
		t.Fatalf("state=%v cooldown=%v", b.State(), b.Cooldown())
	}

	// Probes=1：半开后一次成功即关闭。
	b1, _ := breaker.New(breaker.Config{
		Threshold: 1, MinSamples: 1, FailureRate: 1,
		Cooldown: time.Second, MaxCooldown: time.Minute, Probes: 1, Clock: clk,
	})
	b1.Record(classify.ErrRetryable)
	clk.advance(time.Second)
	if err := b1.Allow(); err != nil {
		t.Fatal(err)
	}
	b1.Record(nil)
	if b1.State() != breaker.Closed {
		t.Fatalf("probes=1 state=%v want closed", b1.State())
	}
}

func TestBreakerHalfOpenProbeConcurrency(t *testing.T) {
	cases := []int{1, 3, 7}
	for _, probes := range cases {
		clk := newFakeClock()
		b, _ := breaker.New(breaker.Config{
			Threshold: 1, MinSamples: 1, FailureRate: 1,
			Cooldown: time.Second, MaxCooldown: time.Minute, Probes: probes, Clock: clk,
		})
		b.Record(classify.ErrRetryable)
		clk.advance(time.Second)
		var passed int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if b.Allow() == nil {
					atomic.AddInt64(&passed, 1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if passed != int64(probes) {
			t.Fatalf("probes=%d passed=%d", probes, passed)
		}
	}
}

func TestBreakerClockBackwards(t *testing.T) {
	clk := newFakeClock()
	b := newTestBreaker(t, clk)
	for i := 0; i < 3; i++ {
		b.Record(classify.ErrRetryable)
	}
	clk.rollback(time.Second)
	if b.RawState() != breaker.Open {
		t.Fatal("should be open")
	}
	// 回拨：Allow 必须返回 ErrClockBackwards 且状态仍是 Open。
	err := b.Allow()
	if !errors.Is(err, breaker.ErrClockBackwards) {
		t.Fatalf("err=%v want clock backwards", err)
	}
	if s := b.State(); s != breaker.Open {
		t.Fatalf("state=%v want open after rollback", s)
	}
	// 推进到恰好冷却点后再回拨：回拨读数仍被拒，不得提前半开。
	clk.advance(11 * time.Second) // 11-1 = 10s，恰好到冷却点
	clk.rollback(time.Nanosecond)
	if err := b.Allow(); !errors.Is(err, breaker.ErrClockBackwards) {
		t.Fatalf("err=%v", err)
	}
	if b.RawState() != breaker.Open {
		t.Fatal("must not half-open on a backwards read")
	}
	// 单调前进越过冷却点后才进入半开。
	clk.advance(time.Second)
	if err := b.Allow(); err != nil {
		t.Fatalf("err=%v", err)
	}
	if b.RawState() != breaker.HalfOpen {
		t.Fatalf("state=%v want half-open", b.State())
	}
}

func TestBreakerTransitionOnce(t *testing.T) {
	cases := []int{50, 200, 1000}
	for _, n := range cases {
		clk := newFakeClock()
		b, _ := breaker.New(breaker.Config{
			Threshold: 1, MinSamples: 1, FailureRate: 1,
			Cooldown: time.Second, MaxCooldown: time.Minute, Probes: 1, Clock: clk,
		})
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				b.OnFailure(classify.Retryable)
			}()
		}
		close(start)
		wg.Wait()
		if b.Transitions() != 1 {
			t.Fatalf("n=%d transitions=%d want 1", n, b.Transitions())
		}
		if b.Cooldown() != time.Second {
			t.Fatalf("cooldown doubled spuriously: %v", b.Cooldown())
		}
		if b.RawState() != breaker.Open {
			t.Fatalf("state=%v", b.RawState())
		}
	}
}

func TestBreakerInvalidConfig(t *testing.T) {
	base := breaker.Config{
		Threshold: 1, MinSamples: 1, FailureRate: 1,
		Cooldown: time.Second, MaxCooldown: time.Minute, Probes: 1,
	}
	mut := []func(*breaker.Config){
		func(c *breaker.Config) { c.Threshold = 0 },
		func(c *breaker.Config) { c.Threshold = -2 },
		func(c *breaker.Config) { c.MinSamples = 0 },
		func(c *breaker.Config) { c.FailureRate = 0 },
		func(c *breaker.Config) { c.FailureRate = 1.5 },
		func(c *breaker.Config) { c.Cooldown = 0 },
		func(c *breaker.Config) { c.MaxCooldown = time.Millisecond },
		func(c *breaker.Config) { c.Probes = -1 },
	}
	for i, m := range mut {
		cfg := base
		m(&cfg)
		if _, err := breaker.New(cfg); err == nil {
			t.Fatalf("case %d should error: %+v", i, cfg)
		}
	}
	if _, err := breaker.New(base); err != nil {
		t.Fatalf("base should be valid: %v", err)
	}
}

func TestBreakerErrorWrapping(t *testing.T) {
	wrapped := fmt.Errorf("gate: %w", breaker.ErrBreakerOpen)
	if !errors.Is(wrapped, breaker.ErrBreakerOpen) {
		t.Fatal("wrapped breaker error not matched")
	}
	if errors.Is(breaker.ErrBreakerOpen, breaker.ErrClockBackwards) {
		t.Fatal("sentinels must be distinct")
	}
}
