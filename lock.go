package ontology

import (
	"fmt"
	"sort"
)

// LockTerm 学期锁定：进行中的申请结案、待审批提案作废，此后仅接受特殊通道。
// 锁定不可撤销；清理顺序按记录键排序，保证重放结果确定。
func (e *Engine) LockTerm(actorID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(actorID, termID) {
		return errf(ErrInvalid, "empty id in lock")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	if e.records[termID] == nil && e.teachers[termID] == nil {
		return errf(ErrNotFound, "unknown term %s", termID)
	}
	if e.locked[termID] {
		return errf(ErrLocked, "term %s already locked (lock is irreversible)", termID)
	}

	recs := e.records[termID]
	keys := make([]string, 0, len(recs))
	for k := range recs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var voided, closed int
	for _, k := range keys {
		rec := recs[k]
		if p := rec.Proposal; p != nil && p.Active {
			p.Active = false
			rec.Proposal = nil
			voided++
			e.appendAudit(AuditLockVoid, actorID, at, rec,
				fmt.Sprintf("voided pending proposal t=%d->%d by %s at term lock", p.CreatedAt, p.Score, p.TeacherID))
		}
		if open := e.openReview(rec); open != nil {
			open.Status = ReviewClosed
			open.ClosedAt = at
			closed++
			e.appendAudit(AuditLockClose, actorID, at, rec, "open review closed at term lock")
		}
		rec.First = nil
	}

	e.locked[termID] = true
	e.advance(at)
	e.appendAuditTerm(AuditLock, actorID, termID, at,
		fmt.Sprintf("term locked; reviews closed=%d, proposals voided=%d; lock irreversible", closed, voided))
	return nil
}

// SpecialFirst 特殊通道第一人确认（仅锁定后可用，不受单次改分上限约束）。
func (e *Engine) SpecialFirst(approverID, studentID, courseID, termID string, score int, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(approverID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in special first")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	if !e.locked[termID] {
		return errf(ErrState, "term %s not locked; special channel unavailable", termID)
	}
	if lvl := e.userLevel(approverID); lvl < e.cfg.MinSpecialLvl {
		return errf(ErrForbidden, "approver level %d below special level %d", lvl, e.cfg.MinSpecialLvl)
	}

	// 第一人确认过期：清除过期确认（不推进时钟、不记审计），同刻可重新确认。
	if rec.First != nil && at > rec.First.ExpiresAt {
		rec.First = nil
	}
	if rec.First != nil {
		return errf(ErrState, "a pending first confirmation by %s exists until t=%d",
			rec.First.ApproverID, rec.First.ExpiresAt)
	}
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return errf(ErrScore, "score %d out of [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}

	cur := e.currentScore(rec)
	rec.First = &PendingFirst{
		ApproverID: approverID,
		At:         at,
		ExpiresAt:  at + e.cfg.FirstValidFor,
	}
	rec.FirstScore = score
	e.advance(at)
	e.appendAudit(AuditSpecialFirst, approverID, at, rec,
		fmt.Sprintf("first confirmation: proposed score %d (current %d, no delta cap); valid until t=%d inclusive",
			score, cur, at+e.cfg.FirstValidFor))
	return nil
}

// SpecialSecond 特殊通道第二人确认：两人不同且第一人确认仍在有效期（终点取等）内则改分生效。
func (e *Engine) SpecialSecond(approverID, studentID, courseID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(approverID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in special second")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	if !e.locked[termID] {
		return errf(ErrState, "term %s not locked; special channel unavailable", termID)
	}
	if lvl := e.userLevel(approverID); lvl < e.cfg.MinSpecialLvl {
		return errf(ErrForbidden, "approver level %d below special level %d", lvl, e.cfg.MinSpecialLvl)
	}

	first := rec.First
	if first == nil {
		return errf(ErrState, "no first confirmation present")
	}
	if approverID == first.ApproverID {
		return errf(ErrForbidden, "two approvers must be distinct; %s is the first approver", approverID)
	}
	if at > first.ExpiresAt {
		rec.First = nil
		return errf(ErrExpired, "first confirmation by %s expired at t=%d (at=%d)",
			first.ApproverID, first.ExpiresAt, at)
	}

	old := e.currentScore(rec)
	score := rec.FirstScore
	firstID := first.ApproverID
	firstAt := first.At
	rec.First = nil
	e.addVersion(rec, score, at, SourceSpecial)
	e.advance(at)
	e.appendAudit(AuditSpecialSecond, approverID, at, rec,
		fmt.Sprintf("second confirmation; first by %s at t=%d still valid at t=%d; score %d->%d effective (special, no delta cap)",
			firstID, firstAt, at, old, score))
	return nil
}

// IsLocked 返回学期是否已锁定。
func (e *Engine) IsLocked(termID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.locked[termID]
}
