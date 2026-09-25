// Package breaker 实现熔断状态机：关闭 → 打开 → 半开。
// 只有真实打到下游的调用才进入失败统计；被拒绝的请求不经由本包计数。
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

// State 是熔断器状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

// Clock 抽象时钟，测试可注入假时钟。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

var (
	// ErrOpen 表示熔断打开（或半开探测名额已满），请求被拒绝。
	ErrOpen = errors.New("breaker: circuit open")
	// ErrClockBackward 表示注入时钟回拨，状态保持不变。
	ErrClockBackward = errors.New("breaker: clock moved backward")
)

// Config 是熔断器配置，所有阈值必须为正。
type Config struct {
	ConsecutiveFailures int           // 连续失败达到该值即打开
	FailureRate         float64       // 失败率阈值，区间 (0, 1]
	MinSamples          int           // 失败率判定的样本数下限
	Cooldown            time.Duration // 打开 → 半开的基础冷却时长
	MaxCooldown         time.Duration // 冷却退避上限
	HalfOpenProbes      int           // 半开期允许的并发探测数
	Clock               Clock         // nil 则用真实时钟
}

// New 校验配置并构造关闭态的熔断器。
func New(cfg Config) (*Breaker, error) {
	if cfg.ConsecutiveFailures <= 0 || cfg.MinSamples <= 0 || cfg.HalfOpenProbes <= 0 {
		return nil, fmt.Errorf("breaker: thresholds must be positive: %+v", cfg)
	}
	if cfg.FailureRate <= 0 || cfg.FailureRate > 1 {
		return nil, fmt.Errorf("breaker: failure rate out of (0,1]: %v", cfg.FailureRate)
	}
	if cfg.Cooldown <= 0 || cfg.MaxCooldown < cfg.Cooldown {
		return nil, fmt.Errorf("breaker: bad cooldown range [%v, %v]", cfg.Cooldown, cfg.MaxCooldown)
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return &Breaker{cfg: cfg, cooldown: cfg.Cooldown}, nil
}

// Breaker 是熔断状态机，全部状态迁移在互斥锁内完成，保证并发下
// 同一触发只迁移一次、冷却只加倍一次。
type Breaker struct {
	mu          sync.Mutex
	cfg         Config
	state       State
	consecutive int
	samples     int
	failures    int
	openedAt    time.Time
	cooldown    time.Duration
	lastNow     time.Time
	inFlight    int // 半开期在途探测数
	succeeded   int // 半开期已成功探测数
	transitions int // 状态迁移总次数（供测试断言只迁移一次）
}

// Allow 判定当前请求是否放行。打开且冷却未到期、或半开探测名额已满时
// 返回 ErrOpen；时钟回拨返回 ErrClockBackward 且状态不变。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.cfg.Clock.Now()
	if now.Before(b.lastNow) {
		return ErrClockBackward
	}
	b.lastNow = now
	if b.state == Open {
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.to(HalfOpen)
	}
	if b.state == HalfOpen {
		if b.inFlight >= b.cfg.HalfOpenProbes {
			return ErrOpen
		}
		b.inFlight++
	}
	return nil
}

// Report 上报一次真实调用的结果（err 为 nil 表示成功）。不可重试错误
// 不计入熔断统计；打开期到达的迟到上报直接忽略。
func (b *Breaker) Report(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case HalfOpen:
		b.inFlight--
		if err == nil {
			b.succeeded++
			if b.succeeded >= b.cfg.HalfOpenProbes {
				b.to(Closed)
			}
		} else {
			b.to(Open) // 半开期任一失败立即回到打开
		}
	case Closed:
		b.countLocked(err)
	}
}

// Abort 撤销一次已放行但未执行的调用（如被舱壁拒绝），
// 仅释放半开探测名额，不改变统计。
func (b *Breaker) Abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen && b.inFlight > 0 {
		b.inFlight--
	}
}

// countLocked 关闭态计数：连续失败达阈值，或样本数达下限且失败率
// 超阈值，二者任一满足即打开。
func (b *Breaker) countLocked(err error) {
	if err == nil {
		b.consecutive = 0
		b.samples++
		return
	}
	if classify.KindOf(err) == classify.NonRetryable {
		return
	}
	b.consecutive++
	b.failures++
	b.samples++
	rate := float64(b.failures) / float64(b.samples)
	if b.consecutive >= b.cfg.ConsecutiveFailures ||
		(b.samples >= b.cfg.MinSamples && rate >= b.cfg.FailureRate) {
		b.to(Open)
	}
}

// to 在持锁状态下迁移状态；半开 → 打开时冷却加倍并封顶，回到关闭时
// 复位计数与冷却。
func (b *Breaker) to(s State) {
	prev := b.state
	b.state = s
	b.transitions++
	switch s {
	case Open:
		b.openedAt = b.cfg.Clock.Now()
		if prev == HalfOpen {
			b.cooldown = min(b.cooldown*2, b.cfg.MaxCooldown)
		}
	case HalfOpen:
		b.inFlight, b.succeeded = 0, 0
	case Closed:
		b.consecutive, b.samples, b.failures = 0, 0, 0
		b.cooldown = b.cfg.Cooldown
	}
}

// State 返回当前状态。
func (b *Breaker) State() State { b.mu.Lock(); defer b.mu.Unlock(); return b.state }

// Transitions 返回状态迁移总次数。
func (b *Breaker) Transitions() int { b.mu.Lock(); defer b.mu.Unlock(); return b.transitions }

// Cooldown 返回当前生效的冷却时长（随退避变化）。
func (b *Breaker) Cooldown() time.Duration { b.mu.Lock(); defer b.mu.Unlock(); return b.cooldown }

// Stats 返回关闭态累计的样本数与失败数（仅供观测失败率口径）。
func (b *Breaker) Stats() (samples, failures int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.samples, b.failures
}
