package activity

import (
	"fmt"

	"ontology/deadline"
	"ontology/retry"
)

// advance 把虚拟状态推演到 now：先处理 Waiting 唤醒，再按最小堆处理到期。
// 每考察一次堆顶 probe 加一：每个到期事件一次，停止前最后一次 Peek 至多一次，
// 故一次处理考察的堆项数 = 到期事件数 + 1（无到期且非 Waiting 时为 1）。
func (a *activity) advance(now int64) {
	a.probe = 0
	for a.state != StateTerminal {
		// Waiting 是纯定时唤醒，不放入堆，避免唤醒事件虚增探测计数。
		if a.state == Waiting {
			if now >= a.waitEnd {
				// 唤醒时刻早于 now：先成为 Scheduled，s2s 与 sc 由堆按时刻重排。
				a.g = a.waitEnd
				a.state = Scheduled
				a.buildScheduled()
				a.trace(now, fmt.Sprintf("wake->scheduled k=%d g=%d", a.k, a.g))
				continue // 下一轮按 Scheduled 的堆统一判定，避免同轮多探一次
			} else {
				// 仍在等待：堆中只剩 sc，交由统一的堆逻辑判定。
				top, ok := a.heap.Peek()
				a.probe++
				if !ok || top.At > now {
					return
				}
				a.heap.Pop()
				a.finish(TermTimedOutSC, SCReason, top.At)
				a.trace(top.At, "expire sc during waiting")
				return
			}
		}

		top, ok := a.heap.Peek()
		a.probe++
		if !ok || top.At > now {
			return
		}
		a.heap.Pop()

		switch top.Kind {
		case deadline.SC:
			a.finish(TermTimedOutSC, SCReason, top.At)
			a.trace(top.At, "expire sc")
			return
		case deadline.S2S:
			a.finish(TermTimedOutS2S, S2S, top.At)
			a.trace(top.At, "expire s2s (no retry)")
			return
		case deadline.S2C:
			a.failAttempt(S2C, top.At)
		case deadline.HB:
			a.failAttempt(HB, top.At)
		}
	}
}

// failAttempt 处理 Running 中一次尝试在时刻 f 的失败。
func (a *activity) failAttempt(rho Reason, f int64) {
	p := a.cfg.policy()
	a.trace(f, fmt.Sprintf("attempt %d failed reason=%v", a.k, rho))
	if !retry.CanRetry(p, a.k) {
		a.finish(TermFailed, rho, f)
		a.trace(f, fmt.Sprintf("terminal failed reason=%v (k=M=%d)", rho, a.k))
		return
	}
	b := retry.Backoff(p, a.k)
	gp := f + b
	if !retry.WithinGlobal(gp, a.t0, a.cfg.SC, a.scSet()) {
		a.finish(TermFailed, rho, f)
		a.trace(f, fmt.Sprintf("terminal failed reason=%v (g'=%d >= sc deadline, no doomed retry)", rho, gp))
		return
	}
	a.k++
	a.state = Waiting
	a.waitEnd = gp
	a.heap = deadline.NewHeap()
	a.setSC(a.heap)
	a.trace(f, fmt.Sprintf("backoff=%d waiting->%d next k=%d", b, gp, a.k))
}

// snapshot 产出当前视图。
func (a *activity) snapshot() Status {
	return Status{
		State: a.state, Attempt: a.k,
		Terminal: a.term, Reason: a.reason, At: a.termAt,
	}
}

func (a *activity) trace(at int64, basis string) {
	if a.log != nil {
		a.log.Printf("[%s] t=%d k=%d state=%v %s", a.id, at, a.k, a.state, basis)
	}
}
