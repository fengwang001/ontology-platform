package notify

import (
	"fmt"

	"ontology/alert"
)

// run 按题面规则逐步朴素执行一条操作，返回输出与判定依据。
func (n *naive) run(o op) nOutcome {
	switch o.kind {
	case kAddTest:
		if o.code == "" || o.low >= o.high || o.step < 1 || o.step > 1_000_000 {
			return nOutcome{err: ErrInvalid, why: "AddTest 参数非法"}
		}
		if _, dup := n.tests[o.code]; dup {
			return nOutcome{err: ErrDuplicate, why: "AddTest 重复"}
		}
		n.tests[o.code] = nTest{o.low, o.high, o.step}
		return nOutcome{why: "AddTest 登记"}
	case kSetWard:
		if o.patient == "" || o.ward == "" {
			return nOutcome{err: ErrInvalid, why: "SetWard 参数非法"}
		}
		n.ward[o.patient] = o.ward
		return nOutcome{why: "SetWard 设当前病区"}
	case kGrant:
		if o.u1 == "" || o.ward == "" || (o.role != Nurse && o.role != Doctor) {
			return nOutcome{err: ErrInvalid, why: "Grant 参数非法"}
		}
		g := n.perm[o.u1]
		if g == nil {
			g = map[grant]struct{}{}
			n.perm[o.u1] = g
		}
		g[grant{o.ward, o.role}] = struct{}{}
		return nOutcome{why: "Grant 授权"}
	}

	// 带 now 的操作：参数非法 > 时钟回退 > 不存在 > 落地 > 无资格 > 状态
	if o.now < 0 || o.now > 1_000_000_000 {
		return nOutcome{err: ErrInvalid, why: "now 越界"}
	}
	switch o.kind {
	case kResult:
		if o.patient == "" || o.code == "" || !validV(o.v) {
			return nOutcome{err: ErrInvalid, why: "Result 参数非法"}
		}
		if o.now < n.maxNow {
			return nOutcome{err: ErrClockBack, why: "时钟回退"}
		}
		if _, ok := n.tests[o.code]; !ok {
			return nOutcome{err: ErrNotFound, why: "项目不存在"}
		}
		if _, ok := n.ward[o.patient]; !ok {
			return nOutcome{err: ErrNotFound, why: "患者不存在"}
		}
	case kNotify:
		if o.event < 1 || o.u1 == "" || o.u2 == "" {
			return nOutcome{err: ErrInvalid, why: "Notify 参数非法"}
		}
		if o.now < n.maxNow {
			return nOutcome{err: ErrClockBack, why: "时钟回退"}
		}
		if e := n.ev(o.event); e == nil {
			return nOutcome{err: ErrNotFound, why: "事件不存在"}
		} else if _, ok := n.ward[e.patient]; !ok {
			return nOutcome{err: ErrNotFound, why: "患者不存在"}
		}
	case kReadBack:
		if o.event < 1 || o.u2 == "" || !validV(o.v) {
			return nOutcome{err: ErrInvalid, why: "ReadBack 参数非法"}
		}
		if o.now < n.maxNow {
			return nOutcome{err: ErrClockBack, why: "时钟回退"}
		}
		if e := n.ev(o.event); e == nil {
			return nOutcome{err: ErrNotFound, why: "事件不存在"}
		} else if _, ok := n.ward[e.patient]; !ok {
			return nOutcome{err: ErrNotFound, why: "患者不存在"}
		}
	case kAct:
		if o.event < 1 || o.u2 == "" {
			return nOutcome{err: ErrInvalid, why: "Act 参数非法"}
		}
		if o.now < n.maxNow {
			return nOutcome{err: ErrClockBack, why: "时钟回退"}
		}
		if e := n.ev(o.event); e == nil {
			return nOutcome{err: ErrNotFound, why: "事件不存在"}
		} else if _, ok := n.ward[e.patient]; !ok {
			return nOutcome{err: ErrNotFound, why: "患者不存在"}
		}
	}

	n.maxNow = o.now
	out := nOutcome{land: n.land(o.now)}

	switch o.kind {
	case kResult:
		sev, crit := n.severity(o.code, o.v)
		if !crit {
			out.normal = true
			out.why = "非危急：不触碰任何事件（复查正常不闭环）"
			return out
		}
		e := n.open[nkey(o.patient, o.code)]
		if e == nil {
			e = &nEvent{
				id:       int64(len(n.events) + 1),
				patient:  o.patient,
				code:     o.code,
				sev:      sev,
				rep:      o.v,
				deadline: o.now + n.t[sev],
				state:    alert.StateNotify,
				nresults: 1,
			}
			n.events = append(n.events, e)
			n.open[nkey(o.patient, o.code)] = e
			out.eid, out.created = e.id, true
			out.sev, out.rep, out.deadline = e.sev, e.rep, e.deadline
			out.why = fmt.Sprintf("危急 sev=%d；新建 ddl=%d", sev, e.deadline)
			return out
		}
		e.nresults++
		if sev > e.sev {
			e.sev = sev
			e.rep = o.v
			e.state = alert.StateNotify
			e.receiver, e.tech, e.mismatch = "", "", 0
			if d := o.now + n.t[sev]; d < e.deadline {
				e.deadline = d
			}
			out.upgraded = true
			out.why = fmt.Sprintf("sev 严格上升：升级，通知作废，ddl=%d 只减不增", e.deadline)
		} else {
			out.why = fmt.Sprintf("sev=%d 不严格大于：仅追加，sev/rep/ddl 不变", sev)
		}
		out.eid, out.sev, out.rep, out.deadline = e.id, e.sev, e.rep, e.deadline
		return out

	case kNotify:
		e := n.ev(o.event)
		w := n.ward[e.patient]
		if !n.hasRole(o.u2, w, Nurse) && !n.hasRole(o.u2, w, Doctor) {
			out.err = ErrUnauthorized
			out.why = "接收人无当前病区护士/医生资格"
			return out
		}
		if e.state != alert.StateNotify {
			out.err = ErrState
			out.why = "事件非待通知"
			return out
		}
		e.state = alert.StateReadBack
		e.tech, e.receiver = o.u1, o.u2
		out.eid = e.id
		out.why = "通知成功：待回读，记接收人"
		return out

	case kReadBack:
		e := n.ev(o.event)
		if e.state != alert.StateReadBack {
			out.err = ErrState
			out.why = "事件非待回读（样例：先报状态不符）"
			return out
		}
		if o.u2 != e.receiver {
			out.err = ErrUnauthorized
			out.why = "回读人非本次通知接收人"
			return out
		}
		out.eid = e.id
		if o.v != e.rep {
			e.mismatch++
			out.mis = true
			if e.mismatch >= 2 {
				e.state = alert.StateNotify
				e.mismatch = 0
				e.receiver, e.tech = "", ""
				out.why = "回读不符累计2次：退回待通知并清零，须重新Notify"
			} else {
				out.why = "回读不符（非错误）：计数+1，仍待回读"
			}
			out.state = e.state
			return out
		}
		e.state = alert.StateAct
		out.state = e.state
		out.why = "回读正确：转待处置"
		return out

	case kAct:
		e := n.ev(o.event)
		w := n.ward[e.patient]
		if !n.hasRole(o.u2, w, Doctor) {
			out.err = ErrUnauthorized
			out.why = "非当前病区医生"
			return out
		}
		if e.state != alert.StateAct {
			out.err = ErrState
			out.why = "事件非待处置"
			return out
		}
		e.state = alert.StateClosed
		delete(n.open, nkey(e.patient, e.code))
		out.eid, out.late = e.id, e.late
		out.why = fmt.Sprintf("处置闭环；late=%v（粘滞逾期）", e.late)
		return out
	}
	return out
}
