// Package breaker 实现 关闭 → 打开 → 半开 三态熔断状态机。
// 时钟可注入；所有状态迁移在互斥锁内完成并计数。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

// ErrOpen 表示熔断打开（冷却期或半开探测额度已满），调用未触达下游。
var ErrOpen = errors.New("breaker: circuit is open")

// ErrClockRollback 表示注入时钟回拨，冷却判定拒绝、状态不变。
var ErrClockRollback = errors.New("breaker: clock moved backwards")

// Clock 是可注入时钟。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// State 是熔断状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	return map[State]string{Closed: "closed", Open: "open", HalfOpen: "half-open"}[s]
}

// Config 是熔断器配置；任一阈值非法时 New 返回错误。
type Config struct {
	ConsecutiveFailures int           // 连续失败达此值 → 打开
	MinSamples          int           // 失败率判定的窗口样本下限
	FailureRate         float64       // 失败率严格大于此值 → 打开
	Cooldown            time.Duration // 基准冷却时长
	MaxCooldown         time.Duration // 冷却加倍上限
	HalfOpenProbes      int           // 半开期允许的并发探测数
}

func (c Config) validate() error {
	switch {
	case c.ConsecutiveFailures <= 0:
		return fmt.Errorf("breaker: ConsecutiveFailures must be > 0, got %d", c.ConsecutiveFailures)
	case c.MinSamples <= 0:
		return fmt.Errorf("breaker: MinSamples must be > 0, got %d", c.MinSamples)
	case c.FailureRate <= 0 || c.FailureRate > 1:
		return fmt.Errorf("breaker: FailureRate must be in (0,1], got %v", c.FailureRate)
	case c.Cooldown <= 0:
		return fmt.Errorf("breaker: Cooldown must be > 0, got %s", c.Cooldown)
	case c.MaxCooldown < c.Cooldown:
		return fmt.Errorf("breaker: MaxCooldown %s < Cooldown %s", c.MaxCooldown, c.Cooldown)
	case c.HalfOpenProbes <= 0:
		return fmt.Errorf("breaker: HalfOpenProbes must be > 0, got %d", c.HalfOpenProbes)
	}
	return nil
}

// Breaker 是熔断器。并发安全。
type Breaker struct {
	mu       sync.Mutex
	clk      Clock
	cfg      Config
	state    State
	consec   int // 关闭态：连续失败数
	winSucc  int // 关闭态：窗口成功数
	winFail  int // 关闭态：窗口失败数
	openedAt time.Time
	cooldown time.Duration
	probes   int // 半开态：在途探测数
	probeSucc int // 半开态：成功探测数
	transitions int // 累计状态迁移次数
}

// New 创建熔断器。clk 为 nil 时使用系统时钟。
func New(cfg Config, clk Clock) (*Breaker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if clk == nil {
		clk = realClock{}
	}
	return &Breaker{clk: clk, cfg: cfg, cooldown: cfg.Cooldown}, nil
}

// Allow 检查此刻能否发起真实调用。打开且冷却未满时返回 ErrOpen；
// 时钟回拨时返回 ErrClockRollback；冷却满则迁移到半开并占一个探测名额。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Open:
		now := b.clk.Now()
		if now.Before(b.openedAt) {
			return ErrClockRollback
		}
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.to(HalfOpen)
		b.probes = 1
		return nil
	case HalfOpen:
		if b.probes >= b.cfg.HalfOpenProbes {
			return ErrOpen
		}
		b.probes++
		return nil
	default:
		return nil
	}
}

// Report 回报一次真实调用的结果。nil 或不可重试错误视为下游健康证据，
// 不计入熔断失败且重置连续失败；可重试错误与超时计入熔断失败。
func (b *Breaker) Report(err error) {
	failure := err != nil && classify.CountsTowardBreaker(classify.Of(err))
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		if err == nil {
			b.consec = 0
			b.winSucc++
			return
		}
		if !failure {
			b.consec = 0
			return
		}
		b.consec++
		b.winFail++
		total := b.winSucc + b.winFail
		rateExceeded := total >= b.cfg.MinSamples &&
			float64(b.winFail)/float64(total) > b.cfg.FailureRate
		if b.consec >= b.cfg.ConsecutiveFailures || rateExceeded {
			b.to(Open)
			b.openedAt = b.clk.Now()
		}
	case HalfOpen:
		if b.probes > 0 {
			b.probes--
		}
		if failure {
			b.cooldown = min(2*b.cooldown, b.cfg.MaxCooldown)
			b.to(Open)
			b.openedAt = b.clk.Now()
			return
		}
		b.probeSucc++
		if b.probeSucc >= b.cfg.HalfOpenProbes {
			b.to(Closed)
		}
	}
}

func (b *Breaker) to(s State) {
	b.state = s
	b.transitions++
	switch s {
	case Closed:
		b.consec, b.winSucc, b.winFail = 0, 0, 0
		b.cooldown = b.cfg.Cooldown
		fallthrough
	case Open, HalfOpen:
		b.probes, b.probeSucc = 0, 0
	}
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Transitions 返回累计状态迁移次数。
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
