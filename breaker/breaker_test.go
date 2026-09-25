package breaker_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/classify"
	"ontology/timeout"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testConfig() breaker.Config {
	return breaker.Config{
		ConsecutiveFailures: 3,
		MinSamples:          10,
		FailureRate:         0.5,
		Cooldown:            time.Second,
		MaxCooldown:         3 * time.Second,
		Probes:              2,
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want classify.Kind
	}{
		{errors.New("x"), classify.Retryable},
		{fmt.Errorf("w: %w", classify.ErrNonRetryable), classify.NonRetryable},
		{fmt.Errorf("w: %w", timeout.ErrTimeout), classify.Timeout},
		{context.DeadlineExceeded, classify.Timeout},
	}
	for _, c := range cases {
		if got := classify.Of(c.err); got != c.want {
			t.Errorf("Of(%v)=%v want %v", c.err, got, c.want)
		}
	}
}

func TestTimeout(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		fn   func() error
		want error // nil 表示期望成功；ErrTimeout 表示超时；其他表示非超时错误
	}{
		{"ok", 100 * time.Millisecond, func() error { return nil }, nil},
		{"slow", 10 * time.Millisecond, func() error { time.Sleep(200 * time.Millisecond); return nil }, timeout.ErrTimeout},
		{"panic", 100 * time.Millisecond, func() error { panic("x") }, errors.New("panic")},
		{"err", 100 * time.Millisecond, func() error { return errors.New("boom") }, errors.New("boom")},
	}
	for _, c := range cases {
		err := timeout.Do(c.d, c.fn)
		switch {
		case c.want == nil && err != nil:
			t.Errorf("%s: err=%v want nil", c.name, err)
		case errors.Is(c.want, timeout.ErrTimeout) && !errors.Is(err, timeout.ErrTimeout):
			t.Errorf("%s: err=%v want ErrTimeout", c.name, err)
		case c.want != nil && !errors.Is(c.want, timeout.ErrTimeout) && (err == nil || errors.Is(err, timeout.ErrTimeout)):
			t.Errorf("%s: err=%v want non-timeout error", c.name, err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	base := testConfig()
	bad := []breaker.Config{}
	c := base
	c.ConsecutiveFailures = 0
	bad = append(bad, c)
	c = base
	c.MinSamples = -1
	bad = append(bad, c)
	c = base
	c.FailureRate = 0
	bad = append(bad, c)
	c = base
	c.Cooldown = 0
	bad = append(bad, c)
	c = base
	c.Probes = 0
	bad = append(bad, c)
	for i, cfg := range bad {
		if _, err := breaker.New(cfg, &fakeClock{}); err == nil {
			t.Errorf("bad config %d accepted", i)
		}
	}
	if _, err := breaker.New(base, &fakeClock{}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		name   string
		script func(t *testing.T, b *breaker.Breaker, clk *fakeClock)
	}{
		{"closed->open consecutive", func(t *testing.T, b *breaker.Breaker, _ *fakeClock) {
			for i := 0; i < 2; i++ {
				b.OnFailure()
			}
			if b.State() != breaker.Closed {
				t.Fatal("opened too early")
			}
			b.OnFailure()
			if b.State() != breaker.Open {
				t.Fatal("not open after 3 consecutive failures")
			}
		}},
		{"closed->open failure rate", func(t *testing.T, b *breaker.Breaker, _ *fakeClock) {
			for i := 0; i < 10; i++ { // 10 样本 6 失败，无连续 3 次
				if i%5 == 4 {
					b.OnSuccess()
				} else {
					b.OnFailure()
				}
			}
			if b.State() != breaker.Open {
				t.Fatalf("state=%v want open (rate 6/10 > 0.5)", b.State())
			}
		}},
		{"open->half-open after cooldown", func(t *testing.T, b *breaker.Breaker, clk *fakeClock) {
			openIt(b)
			if err := b.Allow(); !errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("err=%v want ErrOpen", err)
			}
			clk.advance(time.Second)
			if err := b.Allow(); err != nil {
				t.Fatalf("err=%v want nil (half-open probe)", err)
			}
			if b.State() != breaker.HalfOpen {
				t.Fatalf("state=%v want half-open", b.State())
			}
		}},
		{"half-open->closed all probes ok", func(t *testing.T, b *breaker.Breaker, clk *fakeClock) {
			openIt(b)
			clk.advance(time.Second)
			mustAllow(t, b)
			b.OnSuccess()
			if b.State() != breaker.HalfOpen {
				t.Fatal("closed before all probes succeeded")
			}
			mustAllow(t, b)
			b.OnSuccess()
			if b.State() != breaker.Closed {
				t.Fatalf("state=%v want closed", b.State())
			}
		}},
		{"half-open->open doubles cooldown", func(t *testing.T, b *breaker.Breaker, clk *fakeClock) {
			openIt(b)
			clk.advance(time.Second)
			mustAllow(t, b)
			b.OnFailure()
			if b.State() != breaker.Open || b.Cooldown() != 2*time.Second {
				t.Fatalf("state=%v cooldown=%v want open/2s", b.State(), b.Cooldown())
			}
			clk.advance(2 * time.Second)
			mustAllow(t, b)
			b.OnFailure()
			if b.Cooldown() != 3*time.Second { // 4s 被上限截断为 3s
				t.Fatalf("cooldown=%v want 3s (capped)", b.Cooldown())
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := &fakeClock{now: time.Now()}
			b, err := breaker.New(testConfig(), clk)
			if err != nil {
				t.Fatal(err)
			}
			c.script(t, b, clk)
		})
	}
}

func openIt(b *breaker.Breaker) {
	for i := 0; i < 3; i++ {
		b.OnFailure()
	}
}

func mustAllow(t *testing.T, b *breaker.Breaker) {
	t.Helper()
	if err := b.Allow(); err != nil {
		t.Fatalf("Allow: %v", err)
	}
}

func TestHalfOpenConcurrency(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b, _ := breaker.New(testConfig(), clk)
	openIt(b)
	clk.advance(time.Second)
	var wg sync.WaitGroup
	var okCount, rejectCount atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Allow(); err == nil {
				okCount.Add(1)
			} else {
				rejectCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 2 || rejectCount.Load() != 98 {
		t.Fatalf("ok=%d reject=%d want 2/98", okCount.Load(), rejectCount.Load())
	}
}

func TestClockRewind(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b, _ := breaker.New(testConfig(), clk)
	openIt(b)
	clk.advance(-time.Minute)
	if err := b.Allow(); !errors.Is(err, breaker.ErrClockRewind) {
		t.Fatalf("err=%v want ErrClockRewind", err)
	}
	if b.State() != breaker.Open {
		t.Fatalf("state=%v want open (unchanged)", b.State())
	}
}

func TestConcurrentMigrationOnce(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	cfg := testConfig()
	cfg.ConsecutiveFailures = 1
	b, _ := breaker.New(cfg, clk)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.OnFailure()
		}()
	}
	wg.Wait()
	if b.Transitions() != 1 {
		t.Fatalf("transitions=%d want 1", b.Transitions())
	}
	if b.Cooldown() != time.Second {
		t.Fatalf("cooldown=%v doubled unexpectedly", b.Cooldown())
	}
}
