package ontology

import "fmt"

// CreateProposal 授课教师基于受理中的申请发起改分提案。
func (e *Engine) CreateProposal(teacherID, studentID, courseID, termID string, score int, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(teacherID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in proposal create")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	if e.locked[termID] {
		return errf(ErrLocked, "term %s is locked; use special channel", termID)
	}
	if teacherID != e.teacherOf(rec) {
		return errf(ErrForbidden, "user %s is not teacher of %s/%s", teacherID, termID, courseID)
	}

	// 判定待审批提案时按当前状态（若已过时限，任何待审批提案都会在
	// 后续“是否存在未决提案”检查中被视为已失效）：先做纯状态校验，
	// 仅当操作本身合法时才惰性落地，保证被拒操作零副作用。
	pending := rec.Proposal != nil && rec.Proposal.Active && at <= rec.Proposal.Deadline
	if e.openReview(rec) == nil {
		return errf(ErrState, "no open review to base a proposal on")
	}
	if pending {
		return errf(ErrState, "a pending proposal already exists")
	}
	cur := e.currentScore(rec)
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return errf(ErrScore, "score %d out of [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}
	if d := absInt(score - cur); d > e.cfg.MaxDelta {
		return errf(ErrScore, "|%d-%d|=%d exceeds single-change cap %d", score, cur, d, e.cfg.MaxDelta)
	}

	e.lazyExpire(rec, at)
	rec.Proposal = &Proposal{
		TeacherID: teacherID,
		Score:     score,
		CreatedAt: at,
		Deadline:  at + e.cfg.ApproveTimeout,
		Active:    true,
	}
	e.advance(at)
	e.appendAudit(AuditProposal, teacherID, at, rec,
		fmt.Sprintf("score %d->%d (|delta|<=%d); approve deadline t=%d (inclusive)",
			cur, score, e.cfg.MaxDelta, at+e.cfg.ApproveTimeout))
	return nil
}

// ApproveProposal 审批通过：成绩产生新版本，生效时刻取 at；申请结案。
func (e *Engine) ApproveProposal(approverID, studentID, courseID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(approverID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in proposal approve")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	if e.locked[termID] {
		return errf(ErrLocked, "term %s is locked", termID)
	}

	p := rec.Proposal
	if p == nil {
		return errf(ErrState, "no proposal on record")
	}
	if approverID == p.TeacherID {
		return errf(ErrForbidden, "approver %s is the proposer (self-approval forbidden)", approverID)
	}
	if lvl := e.userLevel(approverID); lvl < e.cfg.MinApproveLvl {
		return errf(ErrForbidden, "approver level %d below required %d", lvl, e.cfg.MinApproveLvl)
	}

	// 惰性失效：恰在时限终点仍有效，at > deadline 才失效。
	if p.Active && at > p.Deadline {
		e.expireProposal(rec, at, "deadline passed before approval")
	}
	if !p.Active {
		return errf(ErrExpired, "proposal created t=%d expired at deadline t=%d", p.CreatedAt, p.Deadline)
	}
	if e.openReview(rec) == nil {
		return errf(ErrState, "backing review is not open")
	}

	old := e.currentScore(rec)
	p.Active = false
	p.Decided = true
	e.addVersion(rec, p.Score, at, SourceReview)
	open := e.openReview(rec)
	open.Status = ReviewApproved
	open.ClosedAt = at
	e.advance(at)
	e.appendAudit(AuditApprove, approverID, at, rec,
		fmt.Sprintf("approved; score %d->%d effective at t=%d; review closed", old, p.Score, at))
	return nil
}

// DenyProposal 审批驳回：提案作废，申请回到受理中。
func (e *Engine) DenyProposal(approverID, studentID, courseID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(approverID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in proposal deny")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	if e.locked[termID] {
		return errf(ErrLocked, "term %s is locked", termID)
	}

	p := rec.Proposal
	if p == nil {
		return errf(ErrState, "no proposal on record")
	}
	if approverID == p.TeacherID {
		return errf(ErrForbidden, "approver %s is the proposer (self-denial forbidden)", approverID)
	}
	if lvl := e.userLevel(approverID); lvl < e.cfg.MinApproveLvl {
		return errf(ErrForbidden, "approver level %d below required %d", lvl, e.cfg.MinApproveLvl)
	}

	if p.Active && at > p.Deadline {
		e.expireProposal(rec, at, "deadline passed before denial")
	}
	if !p.Active {
		return errf(ErrExpired, "proposal created t=%d expired at deadline t=%d", p.CreatedAt, p.Deadline)
	}

	p.Active = false
	p.Decided = true
	rec.Proposal = nil // 申请回到受理中，教师可再次提案
	e.advance(at)
	e.appendAudit(AuditDeny, approverID, at, rec, "proposal denied; review back to open; teacher may repropose")
	return nil
}

// lazyExpire 在触达记录的变更操作前惰性落地超时提案。
// 调用方须持锁。
func (e *Engine) lazyExpire(rec *Record, at int64) {
	if p := rec.Proposal; p != nil && p.Active && at > p.Deadline {
		e.expireProposal(rec, at, fmt.Sprintf("lazy expiry on touching record at t=%d", at))
	}
}

// expireProposal 落地一次超时失效：提案作废，申请回到受理中。调用方须持锁。
func (e *Engine) expireProposal(rec *Record, at int64, reason string) {
	p := rec.Proposal
	p.Active = false
	rec.Proposal = nil
	e.advance(at)
	e.appendAudit(AuditExpire, "system", at, rec,
		fmt.Sprintf("proposal t=%d->%d by %s expired at deadline t=%d: %s; review back to open",
			p.CreatedAt, p.Score, p.TeacherID, p.Deadline, reason))
}
