package review

// Approve 由药师把待审处方置为生效，不重新检查相互作用与极量。
func (e *Engine) Approve(now int, pharmacist string, rxID int) Code {
	return e.judge(now, pharmacist, rxID, StatusActive)
}

// Deny 由药师把待审处方作废并释放占额。
func (e *Engine) Deny(now int, pharmacist string, rxID int) Code {
	return e.judge(now, pharmacist, rxID, StatusDenied)
}

// judge 为 Approve/Deny 的公共流程：
// 参数非法 > 时钟回退 > 处方或人员不存在 > 状态不符。
func (e *Engine) judge(now int, pharmacist string, rxID int, to Status) Code {
	if !validNow(now) || pharmacist == "" || rxID < 0 {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hasNow && now < e.maxNow {
		return ErrClock
	}
	if !e.pharms[pharmacist] || rxID >= len(e.rxs) {
		return ErrNotFound
	}
	r := e.rxs[rxID]
	if e.effStatus(r, now) != StatusPending {
		return ErrState
	}
	e.expire(now)
	e.maxNow, e.hasNow = now, true
	r.status = to
	return OK
}

// Stop 由原开方医生或等级 3 的医生把生效或待审处方每项止日截为 min(e, now)，
// 区间变空的项不再占额也不参与相互作用。
// 拒绝次序：参数非法 > 时钟回退 > 不存在 > 无权限 > 状态不符。
func (e *Engine) Stop(now int, doctor string, rxID int) Code {
	if !validNow(now) || doctor == "" || rxID < 0 {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hasNow && now < e.maxNow {
		return ErrClock
	}
	level, ok := e.doctors[doctor]
	if !ok || rxID >= len(e.rxs) {
		return ErrNotFound
	}
	r := e.rxs[rxID]
	if r.doctor != doctor && level != 3 {
		return ErrPermission
	}
	st := e.effStatus(r, now)
	if st != StatusActive && st != StatusPending {
		return ErrState
	}
	e.expire(now)
	e.maxNow, e.hasNow = now, true
	for i := range r.items {
		if r.items[i].E > now {
			r.items[i].E = now
		}
	}
	return OK
}
