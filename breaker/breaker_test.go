package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/classify"
)

var errFail = errors.New("fail")

type step struct {
	op           string // advance / allow / ok / fail / nonretry
	d            time.Duration
	wantErr      error
	wantState    State
	wantCooldown time.Duration
}

func TestStateMachine(t *testing.T) {
	const c0 = time.Second
	base := Config{ConsecutiveFailures: 2, MinSamples: 4, FailureRate: 0.5,
		Cooldown: c0, MaxCooldown: 4 * c0, HalfOpenProbes: 2}
	scenarios := map[string][]step{
		"连续失败打开→半开→一次成功关闭": {
			{op: "fail", wantState: Closed},
			{op: "fail", wantState: Open, wantCooldown: c0},
			{op: "allow", wantErr: ErrOpen, wantState: Open},
			{op: "advance", d: c0, wantState: Open},
			{op: "allow", wantState: HalfOpen},
			{op: "ok", wantState: HalfOpen}, // probes=2，一次成功未关闭
			{op: "ok", wantState: Closed, wantCooldown: c0},
		},
		"失败率超阈值且样本达下限打开": {
			{op: "fail", wantState: Closed},
			{op: "ok", wantState: Closed},
			{op: "fail", wantState: Closed},
			{op: "fail", wantState: Open}, // 样本4 失败3 率0.75
		},
		"半开失败冷却加倍并封顶": {
			{op: "fail", wantState: Closed},
			{op: "fail", wantState: Open, wantCooldown: c0},
			{op: "advance", d: c0, wantState: Open},
			{op: "allow", wantState: HalfOpen},
			{op: "fail", wantState: Open, wantCooldown: 2 * c0},
			{op: "advance", d: c0, wantState: Open},
			{op: "allow", wantErr: ErrOpen, wantState: Open},
			{op: "advance", d: c0, wantState: Open},
			{op: "allow", wantState: HalfOpen},
			{op: "fail", wantState: Open, wantCooldown: 4 * c0},
			{op: "advance", d: 4 * c0, wantState: Open},
			{op: "allow", wantState: HalfOpen},
			{op: "fail", wantState: Open, wantCooldown: 4 * c0}, // 封顶
		},
		"时钟回拨被拒且状态不变": {
			{op: "fail", wantState: Closed},
			{op: "fail", wantState: Open},
			{op: "advance", d: -time.Hour, wantState: Open},
			{op: "allow", wantErr: ErrClockRollback, wantState: Open},
			{op: "advance", d: time.Hour + c0, wantState: Open},
			{op: "allow", wantState: HalfOpen},
		},
		"不可重试错误不计入样本": {
			{op: "fail", wantState: Closed},
			{op: "nonretry", wantState: Closed},
			{op: "fail", wantState: Open},
		},
	}
	for name, steps := range scenarios {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
			cfg := base
			cfg.Now = func() time.Time { return now }
			b, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for i, s := range steps {
				switch s.op {
				case "advance":
					now = now.Add(s.d)
				case "allow":
					if err := b.Allow(); !errors.Is(err, s.wantErr) {
						t.Fatalf("step %d: Allow err = %v, want %v", i, err, s.wantErr)
					}
				case "ok":
					b.Report(nil)
				case "fail":
					b.Report(errFail)
				case "nonretry":
					b.Report(classify.MarkNonRetryable(errFail))
				}
				if b.State() != s.wantState {
					t.Fatalf("step %d: state = %v, want %v", i, b.State(), s.wantState)
				}
				if s.wantCooldown != 0 && b.cooldown != s.wantCooldown {
					t.Fatalf("step %d: cooldown = %v, want %v", i, b.cooldown, s.wantCooldown)
				}
			}
		})
	}
}

func TestHalfOpenProbeConcurrency(t *testing.T) {
	now := time.Now()
	cfg := Config{ConsecutiveFailures: 1, MinSamples: 1, FailureRate: 1,
		Cooldown: time.Second, MaxCooldown: time.Second, HalfOpenProbes: 3,
		Now: func() time.Time { return now }}
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b.Report(errFail) // 打开
	now = now.Add(time.Second)
	var passed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Allow() == nil {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := passed.Load(); got != 3 {
		t.Errorf("half-open passed = %d, want 3", got)
	}
}

func TestConcurrentTransitionOnce(t *testing.T) {
	cases := []struct {
		name         string
		halfOpen     bool
		wantCooldown time.Duration
	}{
		{"并发失败关闭→打开只迁移一次", false, time.Second},
		{"并发探测失败冷却只加倍一次", true, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			var transitions atomic.Int64
			cfg := Config{ConsecutiveFailures: 1, MinSamples: 1, FailureRate: 1,
				Cooldown: time.Second, MaxCooldown: time.Hour, HalfOpenProbes: 100,
				Now:          func() time.Time { return now },
				OnTransition: func(_, _ State) { transitions.Add(1) }}
			b, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if tc.halfOpen {
				b.Report(errFail)
				now = now.Add(time.Second)
				if err := b.Allow(); err != nil {
					t.Fatalf("Allow: %v", err)
				}
				transitions.Store(0)
			}
			var wg sync.WaitGroup
			for i := 0; i < 50; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); b.Report(errFail) }()
			}
			wg.Wait()
			if got := transitions.Load(); got != 1 {
				t.Errorf("transitions = %d, want 1", got)
			}
			if b.cooldown != tc.wantCooldown {
				t.Errorf("cooldown = %v, want %v", b.cooldown, tc.wantCooldown)
			}
		})
	}
}

func TestInvalidConfig(t *testing.T) {
	ok := Config{ConsecutiveFailures: 1, MinSamples: 1, FailureRate: 0.5,
		Cooldown: time.Second, MaxCooldown: time.Second, HalfOpenProbes: 1}
	with := func(edit func(*Config)) Config { c := ok; edit(&c); return c }
	bad := []Config{{}, {ConsecutiveFailures: -1}, {ConsecutiveFailures: 1},
		with(func(c *Config) { c.FailureRate = 0 }),
		with(func(c *Config) { c.Cooldown = 0 }),
		with(func(c *Config) { c.MaxCooldown = 0 }),
		with(func(c *Config) { c.HalfOpenProbes = 0 }),
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
}
