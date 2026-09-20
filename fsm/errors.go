package fsm

import "errors"

var (
	// ErrNoTransition 表示当前状态下该事件没有对应转移。
	ErrNoTransition = errors.New("fsm: no transition for event in current state")
	// ErrTerminal 表示机器已经处于终态，终态吸收一切事件。
	ErrTerminal = errors.New("fsm: machine is in a terminal state")
	// ErrEntryFailed 表示目标状态的 entry 动作返回了错误，迁移未发生。
	ErrEntryFailed = errors.New("fsm: entry action failed")
)
