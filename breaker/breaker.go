// Package breaker 实现熔断状态机：关闭 → 打开 → 半开。
// 只有真实调用的结果进入失败统计；被拒绝的请求不影响失败率。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

// ErrOpen 表示熔断打开（或半开探测额度已满），请求被拒绝。
var ErrOpen = errors.New("breaker: open")

// ErrClockBackward 表示冷却判定时检测到时钟回拨，状态保持不变。
var ErrClockBackward = errors.New("breaker: clock went backward")

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

// Clock 是可注入的时钟。
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Config 是熔断器配置，所有阈值必须为正。
type Config struct {
	ConsecutiveFailures  int           // 连续失败触发阈值
	FailureRateThreshold float64       // 失败率触发阈值，(0, 1]
	MinSamples           int           // 失败率判定的样本数下限
	Cooldown             time.Duration // 基础冷却时长
	MaxCooldown          time.Duration // 退避加倍后的冷却上限
	Probes               int           // 半开期允许的并发探测数
}

func (c Config) validate() error {
	switch {
	case c.ConsecutiveFailures < 1:
		return fmt.Errorf("breaker: ConsecutiveFailures %d < 1", c.ConsecutiveFailures)
	case c.FailureRateThreshold <= 0 || c.FailureRateThreshold > 1:
		return fmt.Errorf("breaker: FailureRateThreshold %v not in (0,1]", c.FailureRateThreshold)
	case c.MinSamples < 1:
		return fmt.Errorf("breaker: MinSamples %d < 1", c.MinSamples)
	case c.Cooldown <= 0:
		return fmt.Errorf("breaker: Cooldown %v <= 0", c.Cooldown)
	case c.MaxCooldown < c.Cooldown:
		return fmt.Errorf("breaker: MaxCooldown %v < Cooldown %v", c.MaxCooldown, c.Cooldown)
	case c.Probes < 1:
		return fmt.Errorf("breaker: Probes %d < 1", c.Probes)
	}
	return nil
}

// Breaker 是并发安全的熔断器。
type Breaker struct {
	mu   sync.Mutex
	clk  Clock
	cfg  Config
	state State

	consecutive    int // 关闭态连续失败数
	samples        int // 关闭态样本数（真实调用中计入熔断的结果）
	failures       int // 关闭态失败数
	openedAt       time.Time
	cooldown       time.Duration // 当前冷却时长（退避加倍，有上限）
	probesInFlight int
	probeSuccesses int
	transitions    int // 状态迁移次数
}

// New 构造熔断器；clk 为 nil 时使用真实时钟。
func New(cfg Config, clk Clock) (*Breaker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if clk == nil {
		clk = realClock{}
	}
	return &Breaker{clk: clk, cfg: cfg, cooldown: cfg.Cooldown}, nil
}

// Allow 判定请求是否放行。打开态在冷却未满时返回 ErrOpen；检测到时钟
// 回拨返回 ErrClockBackward 且状态不变；半开态仅放行至多 Probes 个探测。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == Open {
		now := b.clk.Now()
		if now.Before(b.openedAt) {
			return ErrClockBackward
		}
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.transitionLocked(HalfOpen)
	}
	if b.state == HalfOpen {
		if b.probesInFlight >= b.cfg.Probes {
			return ErrOpen
		}
		b.probesInFlight++
	}
	return nil
}

// Report 上报一次真实调用的结果（nil 为成功）。不可重试错误不计入熔断。
// 打开态下迟到的上报直接忽略，保证并发下迁移只发生一次。
func (b *Breaker) Report(err error) {
	trips := err != nil && classify.Of(err).Trips()
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		if err == nil {
			b.consecutive = 0
			b.samples++
		} else if trips {
			b.samples++
			b.failures++
			b.consecutive++
		} else {
			return
		}
		rate := float64(b.failures) / float64(b.samples)
		if b.consecutive >= b.cfg.ConsecutiveFailures ||
			(b.samples >= b.cfg.MinSamples && rate >= b.cfg.FailureRateThreshold) {
			b.transitionLocked(Open)
		}
	case HalfOpen:
		if b.probesInFlight > 0 {
			b.probesInFlight--
		}
		if err == nil {
			b.probeSuccesses++
			if b.probeSuccesses >= b.cfg.Probes {
				b.transitionLocked(Closed)
			}
			return
		}
		if trips {
			b.cooldown = min(b.cooldown*2, b.cfg.MaxCooldown)
			b.transitionLocked(Open)
		}
	}
}

// Abort 撤销一次 Allow 放行（如随后被舱壁拒绝），仅在半开态释放探测名额。
func (b *Breaker) Abort() {
	b.mu.Lock()
	if b.state == HalfOpen && b.probesInFlight > 0 {
		b.probesInFlight--
	}
	b.mu.Unlock()
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

func (b *Breaker) transitionLocked(to State) {
	b.state = to
	b.transitions++
	switch to {
	case Open:
		b.openedAt = b.clk.Now()
	case HalfOpen:
		b.probesInFlight = 0
		b.probeSuccesses = 0
	case Closed:
		b.consecutive, b.samples, b.failures = 0, 0, 0
		b.cooldown = b.cfg.Cooldown
	}
}
