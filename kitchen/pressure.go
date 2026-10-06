package kitchen

// pressure 是压单状态机：维护是否在压单、事件记录与严格交替不变量。
//
// 判定滞回规则（w 为重新推定的下一笔假想即时单预计等待）：
//   - w >= 进入阈值：进入压单（若尚未在压单）；
//   - w <  退出阈值：退出压单（若正在压单）；
//   - 其余区间：保持原状态。
//
// 因为退出阈值严格小于进入阈值，单次判定最多产生一个事件，进入与退出严格交替。
type pressure struct {
	pressed bool
	events  []PressureEvent
}

// observeEnter 用于即时单准入判定：该单“加入前”的预计等待 >= 进入阈值。
// 进入条件只由被接受的即时单准入判定驱动；爆单拒单不调用本方法。
func (p *pressure) observeEnter(at int64, wait int64, cfg Config) {
	if !p.pressed && wait >= cfg.EnterThreshold {
		p.append(at, true, "admit immediate: expected wait >= enter threshold", wait)
	}
}

// observeExit 在一次会引起队列变化的被接受操作之后重新推定：
// 准入加入后、完成报告、排队单取消、恢复营业。w < 退出阈值才退出；
// 中间区间保持原状态。
func (p *pressure) observeExit(at int64, wait int64, cfg Config, reason string) {
	if p.pressed && wait < cfg.ExitThreshold {
		p.append(at, false, reason, wait)
	}
}

func (p *pressure) append(at int64, entered bool, reason string, wait int64) {
	// 交替不变量是本状态机的核心可复现性质，任何时候都必须成立。
	if len(p.events) > 0 {
		last := p.events[len(p.events)-1]
		if last.Entered == entered {
			panic("kitchen: pressure events must strictly alternate enter/exit")
		}
	}
	p.pressed = entered
	p.events = append(p.events, PressureEvent{
		Seq:          len(p.events) + 1,
		At:           at,
		Entered:      entered,
		Reason:       reason,
		ExpectedWait: wait,
	})
}

func (p *pressure) snapshotEvents() []PressureEvent {
	out := make([]PressureEvent, len(p.events))
	copy(out, p.events)
	return out
}
