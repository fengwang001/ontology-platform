package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type manClock struct {
	mu sync.Mutex
	t  time.Time
}

func (m *manClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

func (m *manClock) add(d time.Duration) {
	m.mu.Lock()
	m.t = m.t.Add(d)
	m.mu.Unlock()
}

func testCfg(c *manClock) Config {
	return Config{
		Fails: 3, MinSamples: 5, Rate: 0.5,
		Cooldown: 10 * time.Second, MaxCooldown: 40 * time.Second,
		Probes: 2, Window: 20, Clock: c,
	}
}

func TestTransitions(t *testing.T) {
	type tc struct {
		name string
		run  func(t *testing.T, b *Breaker, c *manClock)
	}
	cases := []tc{
		{"invalid config rejected", func(t *testing.T, b *Breaker, c *manClock) {
			bads := []Config{
				{Fails: 0, MinSamples: 1, Rate: .5, Cooldown: time.Second, MaxCooldown: time.Second, Probes: 1, Window: 1},
				{Fails: 1, MinSamples: -1, Rate: .5, Cooldown: time.Second, MaxCooldown: time.Second, Probes: 1, Window: 1},
				{Fails: 1, MinSamples: 1, Rate: 0, Cooldown: time.Second, MaxCooldown: time.Second, Probes: 1, Window: 1},
				{Fails: 1, MinSamples: 1, Rate: .5, Cooldown: 0, MaxCooldown: time.Second, Probes: 1, Window: 1},
				{Fails: 1, MinSamples: 1, Rate: .5, Cooldown: 2 * time.Second, MaxCooldown: time.Second, Probes: 1, Window: 1},
				{Fails: 1, MinSamples: 1, Rate: 2, Cooldown: time.Second, MaxCooldown: time.Second, Probes: 1, Window: 1},
			}
			for _, bc := range bads {
				if _, err := New(bc); !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("expected invalid: %+v err=%v", bc, err)
				}
			}
		}},
		{"consecutive failures open", func(t *testing.T, b *Breaker, c *manClock) {
			for i := 0; i < 3; i++ {
				b.Failure()
			}
			if b.State() != Open {
				t.Fatal("not open after consecutive fails")
			}
			if !errors.Is(b.Allow(), ErrBreakerOpen) {
				t.Fatal("open must reject")
			}
			if b.Cooldown() != 10*time.Second {
				t.Fatalf("cooldown=%v", b.Cooldown())
			}
		}},
		{"failure rate opens at min samples", func(t *testing.T, b *Breaker, c *manClock) {
			cfg := testCfg(c)
			cfg.Fails = 10 // 排除连续失败阈值，只由失败率触发
			b, _ = New(cfg)
			// 5 样本 1 失败：率 0.2 不打开；追加 3 次失败后 4/8=0.5 仍不打开，
			// 再来一次失败 5/9>0.5 打开；此时连续失败仅 4 < 10。
			for i := 0; i < 4; i++ {
				b.Success()
			}
			b.Failure()
			for i := 0; i < 3; i++ {
				b.Failure()
			}
			if b.State() != Closed {
				t.Fatal("rate at threshold must not open")
			}
			b.Failure()
			if b.State() != Open {
				t.Fatal("should open on failure rate")
			}
		}},
		{"cooldown opens to halfopen then success closes", func(t *testing.T, b *Breaker, c *manClock) {
			for i := 0; i < 3; i++ {
				b.Failure()
			}
			c.add(9 * time.Second)
			if b.State() != Open {
				t.Fatal("opened before cooldown")
			}
			c.add(1 * time.Second)
			if b.State() != HalfOpen {
				t.Fatal("not half-open after cooldown")
			}
			if err := b.Allow(); err != nil {
				t.Fatal(err)
			}
			b.Success()
			if b.State() != Closed {
				t.Fatal("one probe success should close")
			}
			if b.Cooldown() != 10*time.Second {
				t.Fatalf("cooldown reset=%v", b.Cooldown())
			}
		}},
		{"halfopen failure doubles cooldown capped", func(t *testing.T, b *Breaker, c *manClock) {
			want := []time.Duration{20 * time.Second, 40 * time.Second, 40 * time.Second}
			for _, w := range want {
				for i := 0; i < 3; i++ {
					b.Failure()
				}
				c.add(b.Cooldown())
				if err := b.Allow(); err != nil {
					t.Fatal(err)
				}
				b.Failure()
				if got := b.Cooldown(); got != w {
					t.Fatalf("cooldown=%v want %v", got, w)
				}
			}
		}},
		{"clock backtrack rejected state unchanged", func(t *testing.T, b *Breaker, c *manClock) {
			for i := 0; i < 3; i++ {
				b.Failure()
			}
			c.add(5 * time.Second)
			_ = b.Allow()
			c.add(-time.Second)
			err := b.Allow()
			if !errors.Is(err, ErrClockBacktrack) {
				t.Fatalf("err=%v", err)
			}
			if b.State() != Open {
				t.Fatal("state changed on backtrack")
			}
		}},
		{"1000 rejections do not affect rate; one success closes",
			func(t *testing.T, b *Breaker, c *manClock) {
				for i := 0; i < 3; i++ {
					b.Failure()
				}
				startTrans := b.Transitions()
				for i := 0; i < 1000; i++ {
					if !errors.Is(b.Allow(), ErrBreakerOpen) {
						t.Fatal("reject expected")
					}
				}
				c.add(10 * time.Second)
				if err := b.Allow(); err != nil {
					t.Fatal(err)
				}
				b.Success()
				if b.State() != Closed {
					t.Fatal("single probe success must close")
				}
				if b.Transitions() <= startTrans {
					t.Fatal("expected transitions")
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &manClock{t: time.Unix(0, 0)}
			b, err := New(testCfg(c))
			if err != nil {
				t.Fatal(err)
			}
			tc.run(t, b, c)
		})
	}
}

func TestHalfOpenProbeConcurrency(t *testing.T) {
	c := &manClock{t: time.Unix(0, 0)}
	cfg := testCfg(c)
	cfg.Probes = 3
	b, _ := New(cfg)
	for i := 0; i < 3; i++ {
		b.Failure()
	}
	c.add(10 * time.Second)

	var allowed, rejected int64
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			if b.Allow() == nil {
				atomic.AddInt64(&allowed, 1)
			} else {
				atomic.AddInt64(&rejected, 1)
			}
		}()
	}
	close(gate)
	wg.Wait()
	if allowed != 3 || rejected != 97 {
		t.Fatalf("allowed=%d rejected=%d", allowed, rejected)
	}
}

func TestTransitionOnce(t *testing.T) {
	c := &manClock{t: time.Unix(0, 0)}
	b, _ := New(testCfg(c))
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Failure() }()
	}
	wg.Wait()
	// 关闭→打开只应发生一次。
	found := int64(0)
	// 迁移计数包含这一次打开。
	if b.Transitions() != 1 {
		t.Fatalf("transitions=%d, want 1", b.Transitions())
	}
	atomic.StoreInt64(&found, 1)
	_ = found
	if b.Cooldown() != 10*time.Second {
		t.Fatalf("cooldown must not double on close->open: %v", b.Cooldown())
	}
}
