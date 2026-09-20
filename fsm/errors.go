package fsm

import "errors"

// 三类拒绝原因，均可用 errors.Is 判定。
var (
	// ErrNoTransition 当前状态下该事件没有对应转移。
	ErrNoTransition = errors.New("fsm: no transition for event in current state")
	// ErrTerminal 机器已处于终态，终态吸收一切事件。
	ErrTerminal = errors.New("fsm: machine is in a terminal state")
	// ErrEntryFailed 目标状态的 entry 动作失败；错误中包裹了动作返回的错误。
	ErrEntryFailed = errors.New("fsm: entry action failed")
)
