// Package fsm 实现一台会话协议状态机。
//
// 状态机由一张转移表驱动：在当前状态上触发事件，若表中存在
// 对应转移则迁移到目标状态，并执行源状态的 exit 动作与目标
// 状态的 entry 动作。机器支持终态吸收、转移日志与状态观察。
package fsm

import "errors"

// State 表示状态机的一个状态。
type State string

// Event 表示驱动状态迁移的事件。
type Event string

// Transition 描述一次从 From 状态经 Event 事件到 To 状态的转移。
type Transition struct {
	From  State
	Event Event
	To    State
}

// 三类拒绝原因，均可通过 errors.Is 判定。
var (
	// ErrNoTransition 表示当前状态下该事件没有对应转移。
	ErrNoTransition = errors.New("fsm: no transition for event in current state")
	// ErrTerminal 表示机器已处于终态，不再接受任何事件。
	ErrTerminal = errors.New("fsm: machine is in a terminal state")
	// ErrEntryFailed 表示目标状态的 entry 动作失败，迁移未发生。
	ErrEntryFailed = errors.New("fsm: entry action failed")
)
