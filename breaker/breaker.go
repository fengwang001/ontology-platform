// Package breaker 实现熔断状态机：关闭 → 打开 → 半开，时钟可注入。
package breaker

import (
	"errors"
	"sync"
	"time"

	"ontology/classify"
)

var (
	// ErrOpen 表示熔断器打开（或半开探测名额已满），调用被拒绝。
	ErrOpen = errors.New("circuit breaker open")
	// ErrClockRegression 表示注入时钟回拨，状态保持不变。
	ErrClockRegression = errors.New("clock regression")
)

// Clock 是可注入时钟。
type Clock interface{ Now() time.Time }

// State 是熔断器状态。
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

// Config 是熔断器配置，所有阈值必须为正。
type Config struct {
	ConsecutiveFailures int           // 连续失败达到该值即打开
	MinSamples          int           // 失败率判定的最小样本数
	FailureRate         float64       // 失败率超过该值即打开（需样本达标）
	HalfOpenProbes      int           // 半开期允许的最大并发探测数
	BaseCooldown        time.Duration // 首次打开的冷却时长
	MaxCooldown         time.Duration // 冷却退避上限
}

func (c Config) validate() error {
	switch {
	case c.ConsecutiveFailures <= 0, c.MinSamples <= 0, c.HalfOpenProbes <= 0:
		return errors.New("breaker: thresholds must be positive")
	case c.FailureRate <= 0 || c.FailureRate > 1:
		return errors.New("breaker: failure rate must be in (0,1]")
	case c.BaseCooldown <= 0 || c.MaxCooldown < c.BaseCooldown:
		return errors.New("breaker: invalid cooldowns")
	}
	return nil
}

const windowSize = 64

// Breaker 是熔断状态机，并发安全。
type Breaker struct {
	cfg Config
	clk Clock

	mu           sync.Mutex
	state        State
	consecutive  int
	window       [windowSize]bool // 最近真实调用的失败环形缓冲
	head, count  int
	failures     int
	openedAt     time.Time
	cooldown     time.Duration
	halfInFlight int
	halfSuccess  int
	transitions  int
}

// New 构造熔断器；配置非法或时钟为 nil 时报错。
func New(cfg Config, clk Clock) (*Breaker, error) {
	if clk == nil {
		return nil, errors.New("breaker: nil clock")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Breaker{cfg: cfg, clk: clk, cooldown: cfg.BaseCooldown}, nil
}

// Allow 判定当前请求是否放行。打开期冷却未到返回 ErrOpen；
// 时钟回拨返回 ErrClockRegression 且状态不变。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == Open {
		now := b.clk.Now()
		if now.Before(b.openedAt) {
			return ErrClockRegression
		}
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.state = HalfOpen
		b.halfInFlight, b.halfSuccess = 0, 0
		b.transitions++
	}
	if b.state == HalfOpen {
		if b.halfInFlight >= b.cfg.HalfOpenProbes {
			return ErrOpen
		}
		b.halfInFlight++
	}
	return nil
}

// Record 上报一次真实调用的结果。err 为 nil 表示成功；
// 是否计为熔断失败由 classify.IsBreakerFailure 决定。
// 打开期间到达的迟到结果直接忽略。
func (b *Breaker) Record(err error) {
	failed := classify.IsBreakerFailure(err)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.push(failed)
		if failed {
			b.consecutive++
		} else {
			b.consecutive = 0
		}
		rate := float64(b.failures) / float64(b.count)
		if b.consecutive >= b.cfg.ConsecutiveFailures ||
			(b.count >= b.cfg.MinSamples && rate > b.cfg.FailureRate) {
			b.openLocked()
		}
	case HalfOpen:
		b.halfInFlight--
		if failed {
			b.cooldown = min(b.cooldown*2, b.cfg.MaxCooldown)
			b.openLocked()
		} else {
			b.halfSuccess++
			if b.halfSuccess >= b.cfg.HalfOpenProbes {
				b.state = Closed
				b.consecutive, b.count, b.failures = 0, 0, 0
				b.cooldown = b.cfg.BaseCooldown
				b.transitions++
			}
		}
	}
}

func (b *Breaker) push(failed bool) {
	if b.count == windowSize && b.window[b.head] {
		b.failures--
	} else if b.count < windowSize {
		b.count++
	}
	b.window[b.head] = failed
	if failed {
		b.failures++
	}
	b.head = (b.head + 1) % windowSize
}

func (b *Breaker) openLocked() {
	b.state = Open
	b.openedAt = b.clk.Now()
	b.consecutive, b.count, b.failures = 0, 0, 0
	b.halfInFlight, b.halfSuccess = 0, 0
	b.transitions++
}

// State 返回当前状态（不触发任何迁移）。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Transitions 返回累计状态迁移次数，供并发正确性断言。
func (b *Breaker) Transitions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.transitions
}

// Cooldown 返回当前生效的冷却时长。
func (b *Breaker) Cooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cooldown
}
