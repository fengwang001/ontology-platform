package gradeaudit

import (
	"fmt"
	"math"
)

func proposalDue(rec *Record, at int64) bool {
	return rec.proposal != nil && rec.proposal.State == proposalPending && at > rec.proposal.DeadlineAt
}

func (e *Engine) expireProposalLocked(rec *Record, at int64) bool {
	if !proposalDue(rec, at) {
		return false
	}
	rec.proposal.State = proposalExpired
	rec.app.State = appOpen
	e.appendAudit(at, AuditProposalExpired, "", rec.Key, rec.proposal.Score, fmt.Sprintf("deadline %d passed before touch at %d", rec.proposal.DeadlineAt, at), "lazy expiration restored application to accepted state")
	return true
}

func (e *Engine) expireSpecialLocked(rec *Record, at int64) bool {
	if rec.special == nil || at <= rec.special.ExpiresAt {
		return false
	}
	session := rec.special
	rec.special = nil
	e.appendAudit(at, AuditProposalExpired, session.FirstActor, rec.Key, session.Score, fmt.Sprintf("first special confirmation expired at %d", session.ExpiresAt), "special channel requires a fresh first confirmation")
	return true
}

func (e *Engine) sweepRecordLocked(rec *Record, at int64) bool {
	return e.expireProposalLocked(rec, at) || e.expireSpecialLocked(rec, at)
}

func (e *Engine) ProposeChange(at int64, teacher ActorID, key RecordKey, score int) error {
	if at < 0 || teacher == "" || !validKey(key) {
		return errorf(ErrInvalid, "proposal requires a time, teacher and complete key")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.begin(at); err != nil {
		return err
	}
	rec := e.records[key]
	if rec == nil {
		return errorf(ErrNotFound, "record %s/%s/%s does not exist", key.Student, key.Course, key.Semester)
	}
	if e.isLocked(key.Semester) {
		return errorf(ErrLocked, "semester %s is locked; use special channel", key.Semester)
	}
	courseTeacher, ok := e.teacher(key.Course)
	if !ok || teacher != courseTeacher {
		return errorf(ErrPermission, "actor %s is not teacher of course %s", teacher, key.Course)
	}

	if e.sweepRecordLocked(rec, at) {
		e.commit(at)
		return errorf(ErrTimeout, "previous pending item expired during this touch")
	}
	if rec.proposal != nil && rec.proposal.State == proposalPending {
		return errorf(ErrState, "record already has a pending proposal")
	}
	if rec.app == nil || rec.app.State != appOpen {
		return errorf(ErrState, "proposal must be based on an accepted application")
	}
	current, ok := versionAt(rec.Versions, at)
	if !ok {
		return errorf(ErrState, "record has no effective score at proposal time")
	}
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return errorf(ErrScore, "proposal score %d is outside [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}
	if int(math.Abs(float64(score-current.Score))) > e.cfg.MaxScoreDelta {
		return errorf(ErrScore, "proposal changes score by more than %d", e.cfg.MaxScoreDelta)
	}
	rec.proposal = &Proposal{
		Teacher:    teacher,
		Score:      score,
		CreatedAt:  at,
		DeadlineAt: at + e.cfg.ApprovalLimit,
		State:      proposalPending,
	}
	rec.app.State = appPending
	e.appendAudit(at, AuditProposal, teacher, key, score, fmt.Sprintf("delta <= %d and score in [%d,%d]", e.cfg.MaxScoreDelta, e.cfg.MinScore, e.cfg.MaxScore), fmt.Sprintf("approval deadline %d", rec.proposal.DeadlineAt))
	e.commit(at)
	return nil
}

func (e *Engine) DecideProposal(at int64, approver ActorID, key RecordKey, approve bool, reason string) error {
	if at < 0 || approver == "" || !validKey(key) {
		return errorf(ErrInvalid, "decision requires a time, approver and complete key")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.begin(at); err != nil {
		return err
	}
	rec := e.records[key]
	if rec == nil {
		return errorf(ErrNotFound, "record %s/%s/%s does not exist", key.Student, key.Course, key.Semester)
	}
	if e.isLocked(key.Semester) {
		return errorf(ErrLocked, "semester %s is locked; ordinary proposal cannot be decided", key.Semester)
	}
	if e.level(approver) < e.cfg.RequiredApproverLevel {
		return errorf(ErrPermission, "approver level %d is below required %d", e.level(approver), e.cfg.RequiredApproverLevel)
	}
	if rec.proposal != nil && rec.proposal.State == proposalPending {
		if approver == rec.proposal.Teacher {
			return errorf(ErrPermission, "proposer cannot approve own proposal")
		}
		if proposalDue(rec, at) {
			e.sweepRecordLocked(rec, at)
			e.commit(at)
			return errorf(ErrTimeout, "proposal expired during decision touch")
		}
	}
	if e.sweepRecordLocked(rec, at) {
		e.commit(at)
		return errorf(ErrTimeout, "another pending item expired during this touch")
	}
	if rec.proposal == nil || rec.proposal.State != proposalPending {
		return errorf(ErrState, "no pending proposal exists")
	}

	if approve {
		rec.Versions = append(rec.Versions, Version{
			Score:  rec.proposal.Score,
			At:     at,
			Source: SourceReview,
		})
		rec.proposal.State = proposalApproved
		rec.app.State = appApproved
		if len(rec.spans) > 0 {
			rec.spans[len(rec.spans)-1].End = at
		}
		e.appendAudit(at, AuditApproval, approver, key, rec.proposal.Score, reason, fmt.Sprintf("approver level %d and deadline %d not passed", e.level(approver), rec.proposal.DeadlineAt))
	} else {
		rec.proposal.State = proposalRejected
		rec.app.State = appOpen
		e.appendAudit(at, AuditProposalReject, approver, key, rec.proposal.Score, reason, "application returned to accepted state")
	}
	e.commit(at)
	return nil
}
