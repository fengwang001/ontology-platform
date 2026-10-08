package xueji

import "fmt"

// QueryAt 时点查询：返回 t 时刻学生的状态、专业及当时是否有未结案申请。
// 只读、不推进时钟；结果由只增不删的版本链与申请历史决定，不受此后操作影响。
// 开销为该学生版本数/申请数的对数，与其他学生总数无关。
func (e *Engine) QueryAt(studentID string, t int64) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.students[studentID]
	if !ok {
		return Snapshot{}, errf(ErrNotFound, "学生 %s 不存在", studentID)
	}
	snap := Snapshot{}
	if v, ok := versionAt(st, t); ok {
		snap.Found = true
		snap.State = v.State
		snap.Major = v.Major
	}
	// 未结案判定：提交时刻 <= t 的最后一份申请，若其在 t 时刻尚未结案
	// 且未超过审批时限（恰等于时限仍有效），则当时处于未结案状态。
	apps := st.apps
	lo, hi := 0, len(apps)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if apps[mid].SubmitTick <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo > 0 {
		a := apps[lo-1]
		closed := a.Status != AppPending && t >= a.CloseTick
		expired := t-a.SubmitTick > e.cfg.TimeLimit
		snap.Pending = !closed && !expired
	}
	return snap, nil
}

// LedgerAt 返回 now 时刻该学生的时长账目：累计休学学期数、累计保留学籍
// 学期数、已用学业年限。只遍历该学生自身版本链，开销与其他学生无关。
func (e *Engine) LedgerAt(studentID string, now int64) (susp, retain, used int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.students[studentID]
	if !ok {
		return 0, 0, 0, errf(ErrNotFound, "学生 %s 不存在", studentID)
	}
	sem := e.cal.At(now)
	if sem < 0 {
		return 0, 0, 0, errf(ErrInvalidParam, "时刻 %d 不在任何学期内", now)
	}
	return e.suspCum(st, sem), e.retCum(st, sem), e.usedYears(st, sem), nil
}

// Audit 返回审计日志副本（按发生顺序）。
func (e *Engine) Audit() []AuditEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]AuditEvent(nil), e.audit...)
}

// Validate 校验任意串行位置上的不变量：
// 1) 每名学生至多一份未结案申请；
// 2) 状态版本链按生效时刻严格递增；
// 3) 休学/保留学籍累计数等于已生效的对应状态学期数（以逐学期重算对照）。
func (e *Engine) Validate(now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	sem := e.cal.At(now)
	for _, st := range e.students {
		pending := 0
		for _, a := range st.apps {
			if a.Status == AppPending {
				pending++
			}
		}
		if pending > 1 {
			return fmt.Errorf("学生 %s 存在 %d 份未结案申请", st.id, pending)
		}
		if (st.pending != nil) != (pending == 1) {
			return fmt.Errorf("学生 %s 未结案申请指针不一致", st.id)
		}
		for i := 1; i < len(st.versions); i++ {
			if st.versions[i].EffectiveTick <= st.versions[i-1].EffectiveTick {
				return fmt.Errorf("学生 %s 版本链未按生效时刻严格递增", st.id)
			}
		}
		if sem < 0 {
			continue
		}
		// 朴素重算：逐学期数状态，与引擎账目对照
		susp, retain := 0, 0
		for s := st.enrollSem; s <= sem; s++ {
			if v, ok := versionAt(st, e.cal.Start(s)); ok {
				switch v.State {
				case StateSuspended:
					susp++
				case StateRetained:
					retain++
				}
			}
		}
		if susp != e.suspCum(st, sem) {
			return fmt.Errorf("学生 %s 休学累计不符: 重算 %d != 账目 %d", st.id, susp, e.suspCum(st, sem))
		}
		if retain != e.retCum(st, sem) {
			return fmt.Errorf("学生 %s 保留学籍累计不符: 重算 %d != 账目 %d", st.id, retain, e.retCum(st, sem))
		}
	}
	return nil
}
