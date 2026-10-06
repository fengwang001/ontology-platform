package ontology

import "fmt"

// EnterScore 初始录入成绩（授课教师）。返回记录键。
func (e *Engine) EnterScore(teacherID, studentID, courseID, termID string, score int, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(teacherID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in enter-score")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	// 课程未登记授课教师 -> 记录不存在类前置错误。
	teacher := ""
	if m := e.teachers[termID]; m != nil {
		teacher = m[courseID]
	}
	if teacher == "" {
		return errf(ErrNotFound, "course %s in term %s has no teacher", courseID, termID)
	}
	if e.locked[termID] {
		return errf(ErrLocked, "term %s is locked", termID)
	}
	if teacher != teacherID {
		return errf(ErrForbidden, "user %s is not teacher of %s/%s", teacherID, termID, courseID)
	}
	if rec := e.getRecord(studentID, courseID, termID); rec != nil {
		return errf(ErrState, "record already exists for %s", describeRecord(rec))
	}
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return errf(ErrScore, "initial score %d out of [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}

	rec := &Record{StudentID: studentID, CourseID: courseID, TermID: termID}
	e.addVersion(rec, score, at, SourceInitial)
	m := e.records[termID]
	if m == nil {
		m = map[string]*Record{}
		e.records[termID] = m
	}
	m[recKeyID(studentID, courseID)] = rec
	e.studentTerm[studentTermKey(termID, studentID)] =
		append(e.studentTerm[studentTermKey(termID, studentID)], rec)
	e.advance(at)
	// 初始录入不进入复核审计账本（审计只记录申请之后的动作链）。
	return nil
}

// ApplyReview 学生在复核窗口内申请复核。
func (e *Engine) ApplyReview(studentID, courseID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in review apply")
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

	// 惰性失效：任何触及记录的变更操作先落地超时提案。
	e.lazyExpire(rec, at)

	initialAt := rec.Versions[0].Effective
	if at > initialAt+e.cfg.ReviewWindow {
		return errf(ErrExpired, "apply at t=%d past review window end t=%d", at, initialAt+e.cfg.ReviewWindow)
	}
	if open := e.openReview(rec); open != nil {
		return errf(ErrState, "an open review already exists since t=%d", open.OpenedAt)
	}

	rec.Reviews = append(rec.Reviews, &Review{OpenedAt: at, Status: ReviewOpen})
	e.advance(at)
	e.appendAudit(AuditReview, studentID, at, rec,
		fmt.Sprintf("accepted; window [%d,%d], at=%d within", initialAt, initialAt+e.cfg.ReviewWindow, at))
	return nil
}

// RejectReview 驳回受理中的复核申请（结案，成绩不变）。
// 若已存在待审批提案，须先由审批人通过或驳回该提案，否则按状态冲突拒绝。
func (e *Engine) RejectReview(actorID, studentID, courseID, termID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(actorID, studentID, courseID, termID) {
		return errf(ErrInvalid, "empty id in review reject")
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
	if actorID != e.teacherOf(rec) && e.userLevel(actorID) < e.cfg.MinApproveLvl {
		return errf(ErrForbidden, "actor %s may not reject review (not teacher, level<%d)", actorID, e.cfg.MinApproveLvl)
	}

	e.lazyExpire(rec, at)

	open := e.openReview(rec)
	if open == nil {
		return errf(ErrState, "no open review to reject")
	}
	if rec.Proposal != nil && rec.Proposal.Active {
		return errf(ErrState, "a pending proposal exists; approve or deny it first")
	}

	open.Status = ReviewRejected
	open.ClosedAt = at
	e.advance(at)
	e.appendAudit(AuditReviewReject, actorID, at, rec, "review rejected; grade unchanged")
	return nil
}
