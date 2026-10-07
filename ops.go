package ontology

import "math"

// 本文件实现各操作的内部逻辑（调用方已持有锁）。
// 所有操作遵循统一的检查顺序以满足错误优先级：
// 参数非法 -> 时钟回退 -> 对象不存在 -> （依赖对象状态的参数检查）-> 状态不符 -> 住宿冲突。

func validTime(t int64) bool { return t >= 0 && t <= MaxTimeMinutes }

// checkClock 校验时钟单调性（只校验，不推进）。
// 时钟只在操作被接受时由 acceptClock 推进，
// 因此被拒绝的操作不改变任何状态与时钟。
func (e *Engine) checkClock(op string, now int64) error {
	if now < e.clock {
		return newErr(op, KindClockRollback, "now 小于上一次被接受操作的 now")
	}
	return nil
}

// acceptClock 在操作被接受时推进时钟。
func (e *Engine) acceptClock(now int64) {
	e.clock = now
}

func (e *Engine) admit(patient, ward string, at, now int64) error {
	const op = "Admit"
	if patient == "" || ward == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return newErr(op, KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.stays.openStay(patient) != nil {
		return newErr(op, KindStateConflict, "该患者存在未出住的住宿记录，须先登记出住")
	}
	// 新区间为 [at, +∞)：任何结束时刻晚于 at 的既有区间都会与之重叠。
	if e.stays.overlapsPatient(patient, at, math.MaxInt64) {
		return newErr(op, KindStayConflict, "与既有住宿区间重叠")
	}
	e.stays.add(&stay{patient: patient, ward: ward, in: at, open: true})
	e.invalidateOpenEnded(patient, ward, at, now)
	e.acceptClock(now)
	return nil
}

func (e *Engine) discharge(patient string, at, now int64) error {
	const op = "Discharge"
	if patient == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return newErr(op, KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if !e.stays.hasStay(patient) {
		return newErr(op, KindNotFound, "该患者没有任何住宿记录")
	}
	open := e.stays.openStay(patient)
	if open == nil {
		return newErr(op, KindStateConflict, "该患者没有未出住的住宿记录")
	}
	if at <= open.in {
		return newErr(op, KindInvalidParam, "出住时刻须晚于入住时刻")
	}
	open.open = false
	open.out = at
	e.invalidateOpenEnded(open.patient, open.ward, at, now)
	e.acceptClock(now)
	return nil
}

func (e *Engine) backfill(patient, ward string, in, out, now int64) error {
	const op = "BackfillStay"
	if patient == "" || ward == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(in) || !validTime(out) || !validTime(now) || out <= in || out > now {
		return newErr(op, KindInvalidParam, "时刻越界、出住不晚于入住或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.stays.overlapsPatient(patient, in, out) {
		return newErr(op, KindStayConflict, "与既有住宿区间重叠")
	}
	e.stays.add(&stay{patient: patient, ward: ward, in: in, out: out})
	e.invalidateStaySpan(patient, ward, in, out, now)
	e.acceptClock(now)
	return nil
}

func (e *Engine) registerCase(caseID, patient string, onset, now int64) error {
	const op = "RegisterCase"
	if caseID == "" || patient == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(onset) || !validTime(now) || onset > now {
		return newErr(op, KindInvalidParam, "发病时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.cases.get(caseID) != nil {
		return newErr(op, KindStateConflict, "病例 ID 已存在")
	}
	e.cases.add(&caseRec{id: caseID, patient: patient, onset: onset, registeredAt: now})
	e.acceptClock(now)
	return nil
}

func (e *Engine) registerIsolation(caseID string, at, now int64) error {
	const op = "RegisterIsolation"
	if caseID == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return newErr(op, KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.cases.get(caseID)
	if c == nil {
		return newErr(op, KindNotFound, "病例不存在")
	}
	if at < c.infectiousStart() {
		return newErr(op, KindInvalidParam, "隔离时刻早于传染期起点")
	}
	if c.revoked {
		return newErr(op, KindStateConflict, "病例已撤销")
	}
	if c.isolated {
		return newErr(op, KindStateConflict, "该病例已登记过隔离")
	}
	c.isolated = true
	c.isolatedAt = at
	e.invalidateCases(map[string]bool{caseID: true})
	e.acceptClock(now)
	return nil
}

func (e *Engine) correctOnset(caseID string, onset, now int64) error {
	const op = "CorrectOnset"
	if caseID == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(onset) || !validTime(now) || onset > now {
		return newErr(op, KindInvalidParam, "发病时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.cases.get(caseID)
	if c == nil {
		return newErr(op, KindNotFound, "病例不存在")
	}
	if c.revoked {
		return newErr(op, KindStateConflict, "病例已撤销")
	}
	c.onset = onset
	e.invalidateCases(map[string]bool{caseID: true})
	e.acceptClock(now)
	return nil
}

func (e *Engine) revokeCase(caseID string, now int64) error {
	const op = "RevokeCase"
	if caseID == "" {
		return newErr(op, KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return newErr(op, KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.cases.get(caseID)
	if c == nil {
		return newErr(op, KindNotFound, "病例不存在")
	}
	if c.revoked {
		return newErr(op, KindStateConflict, "病例已撤销")
	}
	c.revoked = true
	// 清理该病例在 closeOf 中的全部提示。
	for p := range c.close {
		if set := e.closeOf[p]; set != nil {
			delete(set, c.id)
			if len(set) == 0 {
				delete(e.closeOf, p)
			}
		}
	}
	c.close = nil
	c.secondary = nil
	c.valid = false
	e.acceptClock(now)
	return nil
}
