// Package breaker 实现关闭 → 打开 → 半开的熔断状态机，时钟可注入。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrOpen 熔断拒绝（打开期或半开期探测名额已满）。
var ErrOpen = errors.New("breaker open")

// ErrClockRewind 注入时钟回拨到打开时刻之前，状态保持不变。
var ErrClockRewind = errors.New("clock rewind")

// State 熔断状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// Clock 可注入时钟。
type Clock interface{ Now() time.Time }

// ClockFunc 把函数适配为 Clock。
type ClockFunc func() time.Time

// Now 实现 Clock。
func (f ClockFunc) Now() time.Time { return f() }

// Config 熔断配置，所有阈值必须为正。
type Config struct {
	ConsecutiveFailures int           // 连续失败阈值
	MinSamples          int           // 失败率判定的样本下限
	FailureRate         float64       // 失败率阈值，(0,1)
	Cooldown            time.Duration // 基础冷却时长
	MaxCooldown         time.Duration // 退避加倍上限
	Probes              int           // 半开期允许的并发探测数
}

func (c Config) validate() error {
	if c.ConsecutiveFailures < 1 || c.MinSamples < 1 || c.Probes < 1 {
		return fmt.Errorf("breaker: thresholds must be >= 1: %+v", c)
	}
	if c.FailureRate <= 0 || c.FailureRate > 1 {
		return fmt.Errorf("breaker: failure rate %v not in (0,1]", c.FailureRate)
	}
	if c.Cooldown <= 0 || c.MaxCooldown < c.Cooldown {
		return fmt.Errorf("breaker: bad cooldown %v/%v", c.Cooldown, c.MaxCooldown)
	}
	return nil
}

// Breaker 熔断器，并发安全。
type Breaker struct {
	mu    sync.Mutex
	cfg   Config
	clock Clock

	state          State
	consecutive    int
	samples        int
	failures       int
	openedAt       time.Time
	cooldown       time.Duration
	probeInFlight  int
	probeSuccesses int
	transitions    int
}

// New 构造熔断器；配置非法返回错误。
func New(cfg Config, clock Clock) (*Breaker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Breaker{cfg: cfg, clock: clock, cooldown: cfg.Cooldown}, nil
}

// Allow 在真实调用前判定是否放行。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		return nil
	case Open:
		now := b.clock.Now()
		if now.Before(b.openedAt) {
			return ErrClockRewind
		}
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.toHalfOpen()
	}
	// 半开：限制并发探测数
	if b.probeInFlight >= b.cfg.Probes {
		return ErrOpen
	}
	b.probeInFlight++
	return nil
}

// OnSuccess 上报一次真实调用成功。
func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.consecutive = 0
		b.samples++
	case HalfOpen:
		b.probeInFlight--
		b.probeSuccesses++
		if b.probeSuccesses >= b.cfg.Probes {
			b.toClosed()
		}
	}
}

// OnFailure 上报一次真实调用失败。
func (b *Breaker) OnFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.consecutive++
		b.samples++
		b.failures++
		rate := float64(b.failures) / float64(b.samples)
		if b.consecutive >= b.cfg.ConsecutiveFailures ||
			(b.samples >= b.cfg.MinSamples && rate > b.cfg.FailureRate) {
			b.toOpen()
		}
	case HalfOpen:
		b.probeInFlight--
		b.toOpen()
	}
}

func (b *Breaker) toOpen() {
	if b.state == HalfOpen {
		b.cooldown = min(b.cooldown*2, b.cfg.MaxCooldown)
	}
	b.state = Open
	b.openedAt = b.clock.Now()
	b.consecutive, b.samples, b.failures = 0, 0, 0
	b.probeInFlight, b.probeSuccesses = 0, 0
	b.transitions++
}

func (b *Breaker) toHalfOpen() {
	b.state = HalfOpen
	b.probeInFlight, b.probeSuccesses = 0, 0
	b.transitions++
}

func (b *Breaker) toClosed() {
	b.state = Closed
	b.consecutive, b.samples, b.failures = 0, 0, 0
	b.probeInFlight, b.probeSuccesses = 0, 0
	b.cooldown = b.cfg.Cooldown
	b.transitions++
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Cooldown 返回当前冷却时长（含退避加倍）。
func (b *Breaker) Cooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cooldown
}

// Transitions 返回累计状态迁移次数。
func (b *Breaker) Transitions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.transitions
}
