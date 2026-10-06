package alarm

// transitionTrigger 处理“触发”事件，返回新状态与本次是否真正进入激活。
// 触发：正常 / 返回未确认 -> 激活未确认；已激活时为空操作。
func transitionTrigger(s State) (State, bool) {
	switch s {
	case StateNormal, StateReturnUnacked:
		return StateActiveUnacked, true
	default:
		return s, false
	}
}

// transitionReturn 处理“返回”事件，返回新状态与状态是否发生变化。
// 激活未确认 -> 返回未确认；激活已确认 -> 正常；其他为空操作。
func transitionReturn(s State) (State, bool) {
	switch s {
	case StateActiveUnacked:
		return StateReturnUnacked, true
	case StateActiveAcked:
		return StateNormal, true
	default:
		return s, false
	}
}

// transitionAck 处理“确认”，ok 为 false 表示状态不允许。
// 激活未确认 -> 激活已确认；返回未确认 -> 正常。
func transitionAck(s State) (State, bool) {
	switch s {
	case StateActiveUnacked:
		return StateActiveAcked, true
	case StateReturnUnacked:
		return StateNormal, true
	default:
		return s, false
	}
}
