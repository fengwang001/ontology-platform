// Package timer 定时器句柄与状态机：待触发 / 已触发 / 已取消。
// 方法本身不加锁，并发安全由持有方（scheduler）的互斥锁保证。
package timer

import "ontology/slot"

// State 句柄状态。
type State int

const (
	Pending   State = iota // 待触发
	Fired                  // 已触发
	Cancelled              // 已取消
)

// String 返回状态名。
func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Fired:
		return "fired"
	case Cancelled:
		return "cancelled"
	}
	return "unknown"
}

// Timer 定时器句柄。Fn 为触发回调。
type Timer struct {
	state    State
	seq      uint64
	deadline int64
	level    int
	Fn       func()
}

// New 创建待触发句柄，deadline 为绝对到期 tick。
func New(seq uint64, deadline int64, fn func()) *Timer {
	return &Timer{state: Pending, seq: seq, deadline: deadline, Fn: fn}
}

// State 返回当前状态。
func (t *Timer) State() State { return t.state }

// Seq 返回注册序号。
func (t *Timer) Seq() uint64 { return t.seq }

// Deadline 返回绝对到期 tick。
func (t *Timer) Deadline() int64 { return t.deadline }

// Level 返回当前所在层。
func (t *Timer) Level() int { return t.level }

// SetLevel 记录所在层。
func (t *Timer) SetLevel(l int) { t.level = l }

// Cancel 待触发 → 已取消；幂等：非待触发状态返回 false。
func (t *Timer) Cancel() bool {
	if t.state != Pending {
		return false
	}
	t.state = Cancelled
	return true
}

// Fire 待触发 → 已触发；幂等：非待触发状态返回 false。
func (t *Timer) Fire() bool {
	if t.state != Pending {
		return false
	}
	t.state = Fired
	return true
}

// Reset 重设：仅待触发状态可重设，分配新序号与新到期时刻。
func (t *Timer) Reset(seq uint64, deadline int64) bool {
	if t.state != Pending {
		return false
	}
	t.seq, t.deadline = seq, deadline
	return true
}

// Entry 生成对应的槽元素。
func (t *Timer) Entry() slot.Entry {
	return slot.Entry{H: t, Seq: t.seq, Deadline: t.deadline}
}
