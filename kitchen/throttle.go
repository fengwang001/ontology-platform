package kitchen

// PressureEvent 记录压单状态的一次进入或退出，可查询。
// 事件严格交替：进入之后只可能产生退出，反之亦然。
type PressureEvent struct {
	At    int64 // 状态变化发生的时刻
	Enter bool  // true=进入压单，false=退出压单
}

// throttle 是商家的压单状态机。
// 进入：被接受的即时单准入判定预计等待 >= 进入阈值（爆单拒单不改状态）。
// 退出：被接受的准入判定、完成报告或排队单取消之后，重新推定的
// 假想即时单预计等待 < 退出阈值。介于两阈值之间保持原状态。
type throttle struct {
	enterThreshold int64
	exitThreshold  int64
	pressured      bool
	events         []PressureEvent
}

func newThrottle(enterThreshold, exitThreshold int64) throttle {
	return throttle{enterThreshold: enterThreshold, exitThreshold: exitThreshold}
}

// onAdmit 处理一次被接受的即时单准入判定。
func (t *throttle) onAdmit(wait, now int64) {
	if wait >= t.enterThreshold {
		t.transition(true, now)
	} else if wait < t.exitThreshold {
		t.transition(false, now)
	}
}

// onReestimate 处理完成报告或排队单取消之后的重新推定，只可能触发退出。
func (t *throttle) onReestimate(wait, now int64) {
	if wait < t.exitThreshold {
		t.transition(false, now)
	}
}

func (t *throttle) transition(pressured bool, now int64) {
	if t.pressured == pressured {
		return
	}
	t.pressured = pressured
	t.events = append(t.events, PressureEvent{At: now, Enter: pressured})
}
