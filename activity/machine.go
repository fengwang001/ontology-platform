package activity

import "ontology/deadline"

// advance 把 a 推演到 now：处理所有到期时刻 ≤ now 的事件，
// 可跨越 Waiting→Scheduled 的多次尝试。事件在到期时刻本身发生。
func (e *Executor) advance(a *act, now int64) {
	afterWake := false
	for a.state != Terminal && a.heap.Len() > 0 {
		top, _ := a.heap.Peek()
		if !afterWake {
			e.probes++
		}
		afterWake = false
		if top.Time > now {
			return
		}
		a.heap.Pop()
		switch a.state {
		case Scheduled:
			e.logf("advance id=%q @%d 到期 %s -> 终局", a.id, top.Time, top.Kind)
			a.terminalize(kindReason(top.Kind), top.Time)
		case Running:
			e.logf("advance id=%q @%d 到期 %s -> 第 %d 次尝试失败", a.id, top.Time, top.Kind, a.k)
			a.attemptFailed(kindReason(top.Kind), top.Time)
			e.logf("advance id=%q -> state=%d k=%d reason=%q", a.id, a.state, a.k, a.reason)
		case Waiting:
			if top.Kind == deadline.SC {
				e.logf("advance id=%q @%d Waiting 期间 sc 到期 -> 终局", a.id, top.Time)
				a.terminalize(ReasonSC, top.Time)
			} else {
				a.state = Scheduled
				a.rebuildScheduled()
				afterWake = true
				e.logf("advance id=%q @%d 退避结束 -> Scheduled k=%d", a.id, top.Time, a.k)
			}
		}
	}
}

func kindReason(k deadline.Kind) Reason {
	switch k {
	case deadline.SC:
		return ReasonSC
	case deadline.S2S:
		return ReasonS2S
	case deadline.S2C:
		return ReasonS2C
	case deadline.HB:
		return ReasonHB
	default:
		return ReasonApp
	}
}

func (a *act) rebuildScheduled() {
	a.heap = deadline.NewHeap(deadline.ScheduledItems(a.cfg, a.t0, a.g)...)
}

func (a *act) rebuildRunning() {
	a.heap = deadline.NewHeap(deadline.RunningItems(a.cfg, a.t0, a.r, a.h)...)
}

func (a *act) rebuildWaiting(wake int64) {
	a.heap = deadline.NewHeap(deadline.WaitingItems(a.cfg, a.t0, wake)...)
}

func (a *act) terminalize(r Reason, at int64) {
	a.state = Terminal
	a.reason = r
	a.termAt = at
	a.heap = deadline.NewHeap()
}

// attemptFailed 处理第 k 次尝试在时刻 f、原因 r 失败后的预算与排队推演。
func (a *act) attemptFailed(r Reason, f int64) {
	if !a.pol.CanRetry(a.k) {
		a.terminalize(r, f)
		return
	}
	next := a.k + 1
	gp := f + a.pol.Backoff(a.k)
	if a.cfg.SC > 0 && gp >= a.t0+a.cfg.SC {
		a.terminalize(r, f)
		return
	}
	a.k = next
	a.state = Waiting
	a.g = gp
	a.rebuildWaiting(gp)
}
