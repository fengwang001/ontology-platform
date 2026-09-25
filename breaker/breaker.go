// Package breaker 实现关闭 → 打开 → 半开的熔断状态机。
package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Clock 是可注入的时间源。
type Clock interface{ Now() time.Time }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// SystemClock 使用真实墙上时间。
var SystemClock Clock = wallClock{}

var (
	// ErrBreakerOpen 熔断器打开或半开探测名额已满时拒绝。
	ErrBreakerOpen = errors.New("breaker: open, call rejected")
	// ErrClockBacktrack 检测到注入时钟回拨。
	ErrClockBacktrack = errors.New("breaker: clock moved backwards")
	// ErrInvalidConfig 阈值非法。
	ErrInvalidConfig = errors.New("breaker: invalid config")
)

// State 是熔断状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

// Config 描述熔断参数。
type Config struct {
	Fails       int           // 连续失败打开阈值，必须 >0
	MinSamples  int           // 失败率判定的最小样本数，必须 >0
	Rate        float64       // 失败率阈值，(0,1]；严格大于才打开
	Cooldown    time.Duration // 初始冷却，必须 >0
	MaxCooldown time.Duration // 冷却上限，必须 >= Cooldown
	Probes      int           // 半开期并发探测数，必须 >0
	Window      int           // 滚动窗口样本容量，必须 >0
	Clock       Clock         // 缺省用墙上时钟
}

type sample struct{ failed bool }

// Breaker 是单个下游的熔断器。
type Breaker struct {
	cfg       Config
	mu        sync.Mutex
	state     State
	consec    int
	samples   []sample
	failed    int
	openedAt  time.Time
	cooldown  time.Duration
	probesOut int
	lastSeen  time.Time
	trans     int64
}

// New 构造熔断器并校验配置。
func New(cfg Config) (*Breaker, error) {
	if cfg.Fails <= 0 || cfg.MinSamples <= 0 || cfg.Probes <= 0 ||
		cfg.Window <= 0 || cfg.Cooldown <= 0 || cfg.MaxCooldown < cfg.Cooldown ||
		cfg.Rate <= 0 || cfg.Rate > 1 {
		return nil, ErrInvalidConfig
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock
	}
	return &Breaker{cfg: cfg, state: Closed, cooldown: cfg.Cooldown}, nil
}

// State 返回当前状态（会先尝试冷却到期迁移）。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.tryCoolDown(b.cfg.Clock.Now())
	return b.state
}

// Transitions 返回状态迁移发生的累计次数。
func (b *Breaker) Transitions() int64 { return atomic.LoadInt64(&b.trans) }

// Allow 检查是否允许一次真实调用；允许时占用一个探测/普通名额。
// 调用方完成后必须调用 Success 或 Failure。
func (b *Breaker) Allow() error {
	now := b.cfg.Clock.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == Open {
		if err := b.tryCoolDown(now); err != nil {
			return err
		}
	}
	switch b.state {
	case Closed:
		return nil
	case HalfOpen:
		if b.probesOut >= b.cfg.Probes {
			return ErrBreakerOpen
		}
		b.probesOut++
		return nil
	default:
		return ErrBreakerOpen
	}
}

func (b *Breaker) tryCoolDown(now time.Time) error {
	if !b.lastSeen.IsZero() && now.Before(b.lastSeen) {
		return ErrClockBacktrack
	}
	b.lastSeen = now
	if b.state == Open && !now.Before(b.openedAt.Add(b.cooldown)) {
		b.to(HalfOpen, now)
		b.probesOut = 0
	}
	return nil
}

func (b *Breaker) to(s State, now time.Time) {
	if b.state == s {
		return
	}
	b.state = s
	atomic.AddInt64(&b.trans, 1)
	if s == Open {
		b.openedAt = now
	}
}

// Success 上报一次真实调用成功。
func (b *Breaker) Success() {
	now := b.cfg.Clock.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.consec = 0
		b.record(false)
	case HalfOpen:
		b.probesOut--
		if b.probesOut == 0 {
			b.to(Closed, now)
			b.samples, b.failed, b.consec = nil, 0, 0
			b.cooldown = b.cfg.Cooldown
		}
	}
}

// Failure 上报一次真实调用失败。
func (b *Breaker) Failure() {
	now := b.cfg.Clock.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		b.consec++
		b.record(true)
		if b.consec >= b.cfg.Fails ||
			(len(b.samples) >= b.cfg.MinSamples &&
				float64(b.failed)/float64(len(b.samples)) > b.cfg.Rate) {
			b.to(Open, now)
		}
	case HalfOpen:
		if b.probesOut > 0 {
			b.probesOut--
		}
		b.doubleCooldown()
		b.to(Open, now)
	}
}

func (b *Breaker) doubleCooldown() {
	b.cooldown *= 2
	if b.cooldown > b.cfg.MaxCooldown {
		b.cooldown = b.cfg.MaxCooldown
	}
}

func (b *Breaker) record(failed bool) {
	b.samples = append(b.samples, sample{failed})
	if failed {
		b.failed++
	}
	if len(b.samples) > b.cfg.Window {
		if b.samples[0].failed {
			b.failed--
		}
		b.samples = b.samples[1:]
	}
}

// Cooldown 返回当前生效的冷却时长。
func (b *Breaker) Cooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cooldown
}
