// Package breaker 实现 Closed/Open/HalfOpen 熔断状态机。
package breaker

import "ontology/ring"

// State 为熔断状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

// Config 为熔断参数。
type Config struct {
	N, M, F, SR, H int
	S, O           int64
}

// Breaker 为熔断状态机。所有方法均非并发安全，由 guard 持锁串行调用。
type Breaker struct {
	cfg          Config
	state        State
	epoch        int64
	openedAt     int64
	probeIssued  int
	probeSuccess int
	win          *ring.Ring
}

// New 构造状态机。
func New(c Config) *Breaker {
	return &Breaker{
		cfg: c,
		win: ring.New(c.N),
	}
}

// Settle 执行定时迁移（Open 到期且 now>=openedAt+O 转 HalfOpen），返回是否发生迁移。
func (b *Breaker) Settle(now int64) bool {
	if b.state == Open && now >= b.openedAt+b.cfg.O {
		b.state = HalfOpen
		b.epoch++
		b.probeIssued = 0
		b.probeSuccess = 0
		return true
	}
	return false
}

// State 返回当前状态。
func (b *Breaker) State() State { return b.state }

// Epoch 返回当前纪元。
func (b *Breaker) Epoch() int64 { return b.epoch }

// OpenedAt 返回开路时间。
func (b *Breaker) OpenedAt() int64 { return b.openedAt }

// ProbeIssued 返回半开已发探测数。
func (b *Breaker) ProbeIssued() int { return b.probeIssued }

// ProbeSuccess 返回半开成功探测数。
func (b *Breaker) ProbeSuccess() int { return b.probeSuccess }

// ProbeLimit 返回半开探测总数上限 H。
func (b *Breaker) ProbeLimit() int { return b.cfg.H }

// IssueProbe 在 HalfOpen 占用一个探测名额。
func (b *Breaker) IssueProbe() { b.probeIssued++ }

// Ring 返回底层计数滑窗。
func (b *Breaker) Ring() *ring.Ring { return b.win }

// Trip 转入 Open：纪元加一、记录开路时刻、清空环与半开探测计数。
func (b *Breaker) Trip(now int64) {
	b.state = Open
	b.epoch++
	b.openedAt = now
	b.probeIssued = 0
	b.probeSuccess = 0
	b.win.Reset()
}

// ToClosed 转入 Closed：纪元加一、清空环、复位探测计数。
func (b *Breaker) ToClosed() {
	b.state = Closed
	b.epoch++
	b.probeIssued = 0
	b.probeSuccess = 0
	b.win.Reset()
}

// IncProbeSuccess 在 HalfOpen 增加一次成功探测，达到 H 时转 Closed，返回是否已转 Closed。
func (b *Breaker) IncProbeSuccess() bool {
	b.probeSuccess++
	if b.probeSuccess >= b.cfg.H {
		b.ToClosed()
		return true
	}
	return false
}

// ShouldTrip 按当前环内计数判断 Closed 下是否应开路：
// 条数>=M 且 失败数*100>=F*条数 或 慢数*100>=SR*条数。
func (b *Breaker) ShouldTrip() bool {
	n := b.win.Count()
	if n < b.cfg.M {
		return false
	}
	f := b.win.Failed()
	s := b.win.Slow()
	return f*100 >= b.cfg.F*n || s*100 >= b.cfg.SR*n
}
