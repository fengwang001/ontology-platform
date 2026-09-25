// Package breaker 是带注入时钟的熔断状态机：关闭 → 打开 → 半开。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string { return [...]string{"closed", "open", "half-open"}[s] }

type Clock interface{ Now() time.Time }

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

var SystemClock Clock = sysClock{}

var (
	ErrBreakerOpen    = errors.New("breaker: open")
	ErrClockBackwards = errors.New("breaker: clock moved backwards")
)

type Config struct {
	Threshold   int
	MinSamples  int
	FailureRate float64
	Cooldown    time.Duration
	MaxCooldown time.Duration
	Probes      int
	Clock       Clock
}

type Breaker struct {
	cfg Config

	mu          sync.Mutex
	state       State
	consecutive int
	calls       int
	failures    int
	openedAt    time.Time
	cooldown    time.Duration
	probeUsed   int
	probeOK     int
	probeFail   int
	lastNow     time.Time
	transitions int
}

func New(cfg Config) (*Breaker, error) {
	bad := cfg.Threshold <= 0 || cfg.MinSamples <= 0 || cfg.Probes <= 0 ||
		cfg.FailureRate <= 0 || cfg.FailureRate > 1 || cfg.Cooldown <= 0 ||
		cfg.MaxCooldown < cfg.Cooldown
	if bad {
		return nil, errors.New("breaker: invalid config")
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock
	}
	return &Breaker{cfg: cfg, state: Closed, cooldown: cfg.Cooldown}, nil
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	now, err := b.nowLocked()
	if err != nil {
		return b.state
	}
	b.maybeHalfOpenLocked(now)
	return b.state
}

// RawState 返回原始状态，不读取时钟、不触发时间迁移（主要供测试断言）。
func (b *Breaker) RawState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) Transitions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.transitions
}

func (b *Breaker) Cooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cooldown
}

func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now, err := b.nowLocked()
	if err != nil {
		return err
	}
	switch b.state {
	case Closed:
		return nil
	case Open:
		b.maybeHalfOpenLocked(now)
		if b.state != HalfOpen {
			return ErrBreakerOpen
		}
	}
	if b.probeUsed >= b.cfg.Probes {
		return ErrBreakerOpen
	}
	b.probeUsed++
	return nil
}

func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen {
		b.probeOK++
		if b.probeOK+b.probeFail >= b.cfg.Probes {
			b.toClosedLocked()
		}
		return
	}
	b.calls++
	b.consecutive = 0
}

func (b *Breaker) OnFailure(kind classify.Kind) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen {
		b.probeFail++
		b.toOpenLocked() // 探测期任一失败立即回打开（含 Fatal：探测结果坏）
		return
	}
	b.calls++
	if !classify.CountsAsFailure(kind) {
		return // 关闭态：不可重试错误不驱动熔断
	}
	b.failures++
	b.consecutive++
	rate := float64(b.failures) / float64(b.calls)
	if b.state == Closed && (b.consecutive >= b.cfg.Threshold ||
		(b.calls >= b.cfg.MinSamples && rate > b.cfg.FailureRate)) {
		b.toOpenLocked()
	}
}

func (b *Breaker) Record(err error) {
	if err == nil {
		b.OnSuccess()
	} else {
		b.OnFailure(classify.Of(err))
	}
}
func (b *Breaker) nowLocked() (time.Time, error) {
	now := b.cfg.Clock.Now()
	if !b.lastNow.IsZero() && now.Before(b.lastNow) {
		return now, fmt.Errorf("%w: %s before %s", ErrClockBackwards, now, b.lastNow)
	}
	b.lastNow = now
	return now, nil
}
func (b *Breaker) maybeHalfOpenLocked(now time.Time) {
	if b.state == Open && !now.Before(b.openedAt.Add(b.cooldown)) {
		b.state = HalfOpen
		b.probeUsed, b.probeOK, b.probeFail = 0, 0, 0
	}
}

func (b *Breaker) toOpenLocked() {
	if b.state == HalfOpen {
		b.cooldown *= 2
		if b.cooldown > b.cfg.MaxCooldown {
			b.cooldown = b.cfg.MaxCooldown
		}
	}
	b.state = Open
	now := b.lastNow
	b.openedAt = now
	b.consecutive, b.calls, b.failures = 0, 0, 0
	b.transitions++
}

func (b *Breaker) toClosedLocked() {
	b.state = Closed
	b.consecutive, b.calls, b.failures = 0, 0, 0
	b.cooldown = b.cfg.Cooldown
	b.transitions++
}
