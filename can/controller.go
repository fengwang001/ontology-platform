// Package can 实现 CAN 控制器的故障界定状态机。
package can

import (
	"errors"
	"sync"
)

// State 表示节点的故障界定状态。
type State int

const (
	// StateErrorActive 主动错误态。
	StateErrorActive State = iota
	// StateErrorPassive 被动错误态。
	StateErrorPassive
	// StateBusOff 总线关闭态。
	StateBusOff
)

func (s State) String() string {
	switch s {
	case StateErrorActive:
		return "ErrorActive"
	case StateErrorPassive:
		return "ErrorPassive"
	case StateBusOff:
		return "BusOff"
	default:
		return "Unknown"
	}
}

// Event 表示一次发送或接收事件。
type Event int

const (
	// EvTxOK 发送成功。
	EvTxOK Event = iota
	// EvTxErr 发送错误。
	EvTxErr
	// EvTxAckErr 发送时的应答错误。
	EvTxAckErr
	// EvRxOK 接收成功。
	EvRxOK
	// EvRxErr 接收错误。
	EvRxErr
	// EvRxErrDominant 接收到显性错误。
	EvRxErrDominant
)

func (e Event) String() string {
	switch e {
	case EvTxOK:
		return "TxOK"
	case EvTxErr:
		return "TxErr"
	case EvTxAckErr:
		return "TxAckErr"
	case EvRxOK:
		return "RxOK"
	case EvRxErr:
		return "RxErr"
	case EvRxErrDominant:
		return "RxErrDominant"
	default:
		return "Invalid"
	}
}

// Valid 报告事件是否合法。
func (e Event) Valid() bool {
	return e >= EvTxOK && e <= EvRxErrDominant
}

var (
	// ErrInvalidEvent 事件非法。
	ErrInvalidEvent = errors.New("can: invalid event")
	// ErrBusOff 总线关闭时调用 Apply。
	ErrBusOff = errors.New("can: apply rejected in bus-off state")
	// ErrNotBusOff Restart 时不在总线关闭态。
	ErrNotBusOff = errors.New("can: restart rejected, not in bus-off state")
	// ErrAlreadyRecovering Restart 时已在恢复中。
	ErrAlreadyRecovering = errors.New("can: restart rejected, recovery already in progress")
	// ErrNotRecovering Idle11 时不在恢复中。
	ErrNotRecovering = errors.New("can: idle11 rejected, recovery not in progress")
)

// recoveryTarget 是恢复所需的 Idle11 次数。
const recoveryTarget = 128

// Transition 记录一次状态迁移。
type Transition struct {
	// Op 该操作是第几个成功操作（含 Apply、Restart、Idle11，序号从 1 起）。
	Op int
	// From 迁移前状态。
	From State
	// To 迁移后状态。
	To State
}

// Controller 是 CAN 故障界定状态机，所有方法均可并发调用。
type Controller struct {
	mu            sync.Mutex
	tec           int
	rec           int
	recovering    bool
	recoveryCount int
	successOps    int
	transitions   []Transition
}

// NewController 创建初始 TEC=REC=0、主动错误态的控制器。
func NewController() *Controller {
	return &Controller{}
}

// deriveState 由计数导出状态。
func deriveState(tec, rec int) State {
	switch {
	case tec > 255:
		return StateBusOff
	case tec > 127 || rec > 127:
		return StateErrorPassive
	default:
		return StateErrorActive
	}
}

// Apply 按事件发生前的状态处理一次发送/接收事件并更新计数。
// 事件非法或处于总线关闭态时整体拒绝（先判事件非法，再判总线关闭）。
func (c *Controller) Apply(ev Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !ev.Valid() {
		return ErrInvalidEvent
	}
	before := deriveState(c.tec, c.rec)
	if before == StateBusOff {
		return ErrBusOff
	}

	switch ev {
	case EvTxOK:
		if c.tec > 0 {
			c.tec--
		}
	case EvTxErr:
		c.tec += 8
	case EvTxAckErr:
		if before == StateErrorActive {
			c.tec += 8
		}
	case EvRxOK:
		if c.rec > 127 {
			c.rec = 127
		} else if c.rec > 0 {
			c.rec--
		}
	case EvRxErr:
		c.rec++
	case EvRxErrDominant:
		c.rec += 8
	}

	c.successOps++
	c.recordTransitionLocked(before)
	return nil
}

// Restart 在总线关闭且尚未开始恢复时开始恢复，并把恢复计数置 0。
// 先判不在总线关闭，再判已在恢复中。
func (c *Controller) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if deriveState(c.tec, c.rec) != StateBusOff {
		return ErrNotBusOff
	}
	if c.recovering {
		return ErrAlreadyRecovering
	}
	c.recovering = true
	c.recoveryCount = 0
	c.successOps++
	return nil
}

// Idle11 在恢复中累计一次 11 个连续隐性位；累计 128 次时
// TEC 与 REC 清零、状态回到主动错误、恢复结束。
func (c *Controller) Idle11() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.recovering {
		return ErrNotRecovering
	}
	c.recoveryCount++
	c.successOps++
	if c.recoveryCount == recoveryTarget {
		before := deriveState(c.tec, c.rec)
		c.tec = 0
		c.rec = 0
		c.recovering = false
		c.recoveryCount = 0
		c.recordTransitionLocked(before)
	}
	return nil
}

// recordTransitionLocked 在计数更新后记录状态迁移（调用方须持有锁）。
func (c *Controller) recordTransitionLocked(before State) {
	after := deriveState(c.tec, c.rec)
	if after != before {
		c.transitions = append(c.transitions, Transition{
			Op:   c.successOps,
			From: before,
			To:   after,
		})
	}
}

// TEC 返回当前发送错误计数。
func (c *Controller) TEC() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tec
}

// REC 返回当前接收错误计数。
func (c *Controller) REC() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec
}

// State 返回由当前计数导出的状态。
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return deriveState(c.tec, c.rec)
}

// Recovering 报告是否处于总线关闭恢复中。
func (c *Controller) Recovering() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recovering
}

// RecoveryCount 返回当前恢复计数。
func (c *Controller) RecoveryCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recoveryCount
}

// SuccessOps 返回已成功操作（Apply、Restart、Idle11）的总数。
func (c *Controller) SuccessOps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.successOps
}

// Transitions 返回迁移记录的副本。
func (c *Controller) Transitions() []Transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Transition, len(c.transitions))
	copy(out, c.transitions)
	return out
}
