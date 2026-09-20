// Package fsm 实现一台会话协议状态机。
//
// 状态机由初始状态、终态集合与转移表构造，通过 Fire 驱动迁移，
// 支持在状态上注册 entry/exit 动作、查询转移日志以及订阅状态变更。
// 所有方法均可被多个 goroutine 并发调用。
package fsm

import "errors"

// State 表示状态机的一个状态。
type State string

// Event 表示驱动状态迁移的事件。
type Event string

// Transition 描述一次转移：在 From 状态下收到 Event 后迁移到 To。
// From 与 To 相同的自转移是合法的。
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
	// ErrEntryFailed 表示目标状态的 entry 动作失败，机器停留在原状态。
	ErrEntryFailed = errors.New("fsm: entry action failed")
)

// transitionKey 是转移表的查找键。
type transitionKey struct {
	from  State
	event Event
}
