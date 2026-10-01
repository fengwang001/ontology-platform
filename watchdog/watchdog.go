// Package watchdog 实现一个带注入时钟的窗口看门狗模型。
//
// 喂狗窗口为自上次喂狗时刻 f 起的 [open, close)（左闭右开）：
// d < open 为过早喂狗（立即 Early 复位），open <= d < close 为有效喂狗，
// d 达到 close 时在 f+close 时刻 Timeout 复位。pre > 0 时在 f+close-pre
// 时刻产生一次预警。所有操作先把到点（时刻不大于操作时刻）的预警与超时
// 复位按时刻先后补记入事件表，再处理操作本身。
package watchdog

import (
	"errors"
	"sync"
)

// Reason 是复位原因。
type Reason string

const (
	// Early 表示过早喂狗导致的复位（d < open）。
	Early Reason = "Early"
	// Timeout 表示窗口走完未喂狗导致的复位（d 达到 close）。
	Timeout Reason = "Timeout"
)

// Kind 是事件种类。
type Kind string

const (
	// Warning 是窗口关闭前 pre 个时间单位的预警事件。
	Warning Kind = "Warning"
	// Reset 是复位事件（Early 或 Timeout）。
	Reset Kind = "Reset"
)

// Event 是事件表中的一条记录。同一周期内预警先于复位；
// Early 复位与预警同刻时二者时刻相等，预警仍排在前面。
type Event struct {
	Time   int64
	Kind   Kind
	Reason Reason // Kind == Reset 时为 Early 或 Timeout，否则为空
}

var (
	// ErrInvalidConfig 表示构造参数不合法。
	ErrInvalidConfig = errors.New("watchdog: invalid config")
	// ErrTimeRewound 表示操作时刻早于上一次操作时刻（首个操作与 t0 比较）。
	ErrTimeRewound = errors.New("watchdog: operation time rewound")
	// ErrFeedWhileReset 表示复位态下喂狗被拒。
	ErrFeedWhileReset = errors.New("watchdog: feed rejected: watchdog is in reset state")
	// ErrFeedTooEarly 表示喂狗过早（d < open），同时已触发 Early 复位。
	ErrFeedTooEarly = errors.New("watchdog: feed rejected: too early")
	// ErrRestartNotReset 表示非复位态下 Restart 被拒。
	ErrRestartNotReset = errors.New("watchdog: restart rejected: not in reset state")
)

// Clock 是可注入的时钟。Now 必须单调不减地返回整数时刻。
type Clock interface {
	Now() int64
}

// ClockFunc 让普通函数满足 Clock 接口。
type ClockFunc func() int64

func (f ClockFunc) Now() int64 { return f() }

// Watchdog 是并发安全的窗口看门狗。零值不可用，须用 New 构造。
type Watchdog struct {
	mu sync.Mutex

	open  int64
	close int64
	pre   int64

	f          int64 // 上次喂狗时刻 / 当前周期起点
	reset      bool  // 是否锁存在复位态
	lastOpTime int64 // 上一次被接受的操作时刻，首个操作与 t0 比较

	warnDue   int64 // 本周期预警时刻
	warnFired bool  // 本周期预警是否已记录

	events []Event
}

// New 构造看门狗。要求 0 <= open < close 且 0 <= pre < close。
func New(t0, open, closeAt, pre int64) *Watchdog {
	if open < 0 || closeAt < 0 || pre < 0 || open >= closeAt || pre >= closeAt {
		panic(ErrInvalidConfig)
	}
	w := &Watchdog{
		open:       open,
		close:      closeAt,
		pre:        pre,
		f:          t0,
		lastOpTime: t0,
	}
	w.arm(t0)
	return w
}

// arm 以 f 为周期起点重新武装预警。调用方须持有 w.mu。
func (w *Watchdog) arm(f int64) {
	w.f = f
	if w.pre > 0 {
		w.warnDue = f + w.close - w.pre
	} else {
		w.warnDue = 0 // pre == 0 时本周期不预警
	}
	w.warnFired = false
}

// catchUp 把时刻不大于 t 且尚未记录的预警与超时复位按时刻先后补记。
// 同一周期预警时刻严格早于超时复位（warnDue = f+close-pre < f+close），
// 因此依次至多追加这两条；复位后锁存，不再产生任何事件。
// 调用方须持有 w.mu。
func (w *Watchdog) catchUp(t int64) {
	if w.reset {
		return
	}
	timeoutAt := w.f + w.close
	if w.pre > 0 && !w.warnFired && w.warnDue <= t {
		w.events = append(w.events, Event{Time: w.warnDue, Kind: Warning})
		w.warnFired = true
	}
	if timeoutAt <= t {
		w.events = append(w.events, Event{Time: timeoutAt, Kind: Reset, Reason: Timeout})
		w.reset = true
	}
}

// checkTime 校验时刻不回退：首个操作与 t0 比较，之后与上一次操作时刻比较。
// 回退不改变任何状态。调用方须持有 w.mu。
func (w *Watchdog) checkTime(t int64) error {
	if t < w.lastOpTime {
		return ErrTimeRewound
	}
	return nil
}

// advance 接受本次操作时刻（回退的操作除外）。调用方须持有 w.mu。
func (w *Watchdog) advance(t int64) { w.lastOpTime = t }

// FeedAt 在时刻 t 喂狗：先补记到点事件，再判定过早/有效/复位态。
func (w *Watchdog) FeedAt(t int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.checkTime(t); err != nil {
		return err
	}
	defer w.advance(t)
	w.catchUp(t)
	if w.reset {
		return ErrFeedWhileReset
	}
	d := t - w.f
	if d < w.open {
		// 过早喂狗：这是唯一会改变看门狗状态的拒绝。
		// 若预警与 Early 复位同刻（warnDue == t），预警已在 catchUp 中先记录。
		w.events = append(w.events, Event{Time: t, Kind: Reset, Reason: Early})
		w.reset = true
		return ErrFeedTooEarly
	}
	// open <= d < close：catchUp 已保证 d < close（否则已 Timeout 复位）。
	w.arm(t)
	return nil
}

// TickAt 在时刻 t 仅补记到点事件，不改变看门狗的武装状态。
func (w *Watchdog) TickAt(t int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.checkTime(t); err != nil {
		return err
	}
	defer w.advance(t)
	w.catchUp(t)
	return nil
}

// RestartAt 在时刻 t 先补记（超时可能恰在此时到点而进入复位态），
// 仅当补记后处于复位态时才以 t 为新的 f 重新武装。
func (w *Watchdog) RestartAt(t int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.checkTime(t); err != nil {
		return err
	}
	defer w.advance(t)
	w.catchUp(t)
	if !w.reset {
		return ErrRestartNotReset
	}
	w.reset = false
	w.arm(t)
	return nil
}

// Feed 使用注入时钟的当前时刻调用 FeedAt。
func (w *Watchdog) Feed(clock Clock) error { return w.FeedAt(clock.Now()) }

// Tick 使用注入时钟的当前时刻调用 TickAt。
func (w *Watchdog) Tick(clock Clock) error { return w.TickAt(clock.Now()) }

// Restart 使用注入时钟的当前时刻调用 RestartAt。
func (w *Watchdog) Restart(clock Clock) error { return w.RestartAt(clock.Now()) }

// Events 返回事件表的快照副本，时刻非递减。
func (w *Watchdog) Events() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Event, len(w.events))
	copy(out, w.events)
	return out
}

// InReset 返回看门狗当前是否锁存在复位态。
func (w *Watchdog) InReset() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reset
}
