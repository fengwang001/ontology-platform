// Package breaker 实现熔断状态机：关闭 → 打开 → 半开。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

// 哨兵错误，可用 errors.Is 判定。
var (
	// ErrOpen 表示熔断器拒绝本次调用（打开中或半开探测名额已满）。
	ErrOpen = errors.New("breaker: open")
	// ErrClockRollback 表示注入时钟回拨，状态保持不变。
	ErrClockRollback = errors.New("breaker: clock rollback")
)

// State 是熔断器状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	}
	return "unknown"
}

// Config 是熔断器配置。
type Config struct {
	ConsecutiveFailures int           // 连续失败达此值即打开，必须 > 0
	MinSamples          int           // 失败率判定的样本下限，必须 > 0
	FailureRate         float64       // 失败率阈值，须在 (0,1]
	Cooldown            time.Duration // 基准冷却时长，必须 > 0
	MaxCooldown         time.Duration // 冷却上限，必须 >= Cooldown
	HalfOpenProbes      int           // 半开期允许的并发探测数，必须 > 0
	Now                 func() time.Time
	// OnTransition 在状态迁移时同步调用（持锁期间），不得回调熔断器。
	OnTransition func(from, to State)
}

// Breaker 是熔断器。只有真实调用的结果才应通过 Report 上报。
type Breaker struct {
	cfg Config

	mu             sync.Mutex
	state          State
	consecFails    int
	samples        int
	fails          int
	openedAt       time.Time
	cooldown       time.Duration
	probesInFlight int
	probeSuccesses int
}

// New 构造熔断器；任一阈值非法（<= 0、比率越界、冷却倒挂）即报错。
func New(cfg Config) (*Breaker, error) {
	switch {
	case cfg.ConsecutiveFailures <= 0:
		return nil, fmt.Errorf("breaker: ConsecutiveFailures must be > 0, got %d", cfg.ConsecutiveFailures)
	case cfg.MinSamples <= 0:
		return nil, fmt.Errorf("breaker: MinSamples must be > 0, got %d", cfg.MinSamples)
	case cfg.FailureRate <= 0 || cfg.FailureRate > 1:
		return nil, fmt.Errorf("breaker: FailureRate must be in (0,1], got %v", cfg.FailureRate)
	case cfg.Cooldown <= 0:
		return nil, fmt.Errorf("breaker: Cooldown must be > 0, got %v", cfg.Cooldown)
	case cfg.MaxCooldown < cfg.Cooldown:
		return nil, fmt.Errorf("breaker: MaxCooldown %v < Cooldown %v", cfg.MaxCooldown, cfg.Cooldown)
	case cfg.HalfOpenProbes <= 0:
		return nil, fmt.Errorf("breaker: HalfOpenProbes must be > 0, got %d", cfg.HalfOpenProbes)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.OnTransition == nil {
		cfg.OnTransition = func(State, State) {}
	}
	return &Breaker{cfg: cfg, state: Closed, cooldown: cfg.Cooldown}, nil
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Allow 判定本次调用是否放行。打开期未过冷却返回 ErrOpen；
// 时钟回拨返回 ErrClockRollback 且状态不变；半开期探测名额满返回 ErrOpen。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		return nil
	case Open:
		now := b.cfg.Now()
		if now.Before(b.openedAt) {
			return ErrClockRollback
		}
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.transition(HalfOpen)
		b.probesInFlight = 1
		return nil
	default: // HalfOpen
		if b.probesInFlight >= b.cfg.HalfOpenProbes {
			return ErrOpen
		}
		b.probesInFlight++
		return nil
	}
}

// Report 上报一次真实调用的结果（nil 为成功）。
// 不可重试错误不计入熔断样本；被拒绝的调用不得上报。
func (b *Breaker) Report(err error) {
	if err != nil && classify.Of(err) == classify.NonRetryable {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.samples++
		if err == nil {
			b.consecFails = 0
			return
		}
		b.fails++
		b.consecFails++
		rateHit := b.samples >= b.cfg.MinSamples &&
			float64(b.fails)/float64(b.samples) >= b.cfg.FailureRate
		if b.consecFails >= b.cfg.ConsecutiveFailures || rateHit {
			b.openLocked()
		}
	case HalfOpen:
		b.probesInFlight--
		if err == nil {
			b.probeSuccesses++
			if b.probeSuccesses >= b.cfg.HalfOpenProbes {
				b.closeLocked()
			}
		} else {
			b.openLocked()
		}
	default: // Open：真实调用不可能处于此态，防御性忽略
	}
}

func (b *Breaker) openLocked() {
	if b.state == HalfOpen { // 探测失败：冷却加倍，封顶
		b.cooldown = min(2*b.cooldown, b.cfg.MaxCooldown)
	}
	b.openedAt = b.cfg.Now()
	b.probesInFlight = 0
	b.probeSuccesses = 0
	b.transition(Open)
}

func (b *Breaker) closeLocked() {
	b.consecFails = 0
	b.samples = 0
	b.fails = 0
	b.cooldown = b.cfg.Cooldown
	b.probesInFlight = 0
	b.probeSuccesses = 0
	b.transition(Closed)
}

func (b *Breaker) transition(to State) {
	from := b.state
	b.state = to
	b.cfg.OnTransition(from, to)
}
