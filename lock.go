package gradeaudit

import "fmt"

func (e *Engine) LockSemester(at int64, actor ActorID, semester SemesterID) error {
	if at < 0 || actor == "" || semester == "" {
		return errorf(ErrInvalid, "lock requires a time, actor and semester")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.begin(at); err != nil {
		return err
	}
	if _, locked := e.locked[semester]; locked {
		return errorf(ErrLocked, "semester %s is already locked", semester)
	}
	if e.level(actor) < e.cfg.RequiredLockLevel {
		return errorf(ErrPermission, "actor level %d is below lock level %d", e.level(actor), e.cfg.RequiredLockLevel)
	}

	for _, rec := range sortedRecords(e.records) {
		if rec.Key.Semester != semester {
			continue
		}
		e.sweepRecordLocked(rec, at)
		if rec.proposal != nil && rec.proposal.State == proposalPending {
			rec.proposal.State = proposalExpired
			if rec.app != nil {
				rec.app.State = appLocked
			}
			if len(rec.spans) > 0 && rec.spans[len(rec.spans)-1].End == 0 {
				rec.spans[len(rec.spans)-1].End = at
			}
			e.appendAudit(at, AuditProposalReject, actor, rec.Key, rec.proposal.Score, "semester lock", "pending proposal voided before lock took effect")
		} else if rec.app != nil && (rec.app.State == appOpen || rec.app.State == appPending) {
			rec.app.State = appLocked
			if len(rec.spans) > 0 && rec.spans[len(rec.spans)-1].End == 0 {
				rec.spans[len(rec.spans)-1].End = at
			}
			e.appendAudit(at, AuditApplicationReject, actor, rec.Key, 0, "semester lock", "open application closed before lock took effect")
		}
	}

	e.locked[semester] = at
	e.appendAudit(at, AuditLock, actor, RecordKey{Semester: semester}, 0, "lock is irreversible", "applications and ordinary proposals are no longer accepted")
	e.commit(at)
	return nil
}

func (e *Engine) SpecialConfirm(at int64, approver ActorID, key RecordKey, score int) (bool, error) {
	if at < 0 || approver == "" || !validKey(key) {
		return false, errorf(ErrInvalid, "special confirmation requires a time, approver and complete key")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.begin(at); err != nil {
		return false, err
	}
	rec := e.records[key]
	if rec == nil {
		return false, errorf(ErrNotFound, "record %s/%s/%s does not exist", key.Student, key.Course, key.Semester)
	}
	lockedAt, locked := e.locked[key.Semester]
	if !locked {
		return false, errorf(ErrLocked, "special channel is available only after semester lock")
	}
	if e.level(approver) < e.cfg.SpecialApproverLevel {
		return false, errorf(ErrPermission, "actor level %d is below special level %d", e.level(approver), e.cfg.SpecialApproverLevel)
	}

	if rec.special != nil {
		session := rec.special
		if session.Score != score {
			return false, errorf(ErrInvalid, "second confirmation score %d does not match first score %d", score, session.Score)
		}
		if approver == session.FirstActor {
			return false, errorf(ErrPermission, "two different special approvers are required")
		}
		if at > session.ExpiresAt {
			e.sweepRecordLocked(rec, at)
			e.commit(at)
			return false, errorf(ErrTimeout, "first special confirmation expired at %d", session.ExpiresAt)
		}
		if score < e.cfg.MinScore || score > e.cfg.MaxScore {
			return false, errorf(ErrScore, "special score %d is outside [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
		}

		rec.Versions = append(rec.Versions, Version{Score: score, At: at, Source: SourceSpecial})
		firstActor := session.FirstActor
		firstAt := session.FirstAt
		rec.special = nil
		e.appendAudit(at, AuditSpecialSecond, approver, key, score, fmt.Sprintf("locked at %d; first approver %s at %d was still valid", lockedAt, firstActor, firstAt), "special confirmation created a new score version")
		e.commit(at)
		return true, nil
	}

	if e.sweepRecordLocked(rec, at) {
		e.commit(at)
		return false, errorf(ErrTimeout, "an expired pending item was landed during this touch")
	}
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return false, errorf(ErrScore, "special score %d is outside [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}
	rec.special = &specialSession{
		FirstActor: approver,
		Score:      score,
		FirstAt:    at,
		ExpiresAt:  at + e.cfg.SpecialConfirmTTL,
	}
	e.appendAudit(at, AuditSpecialFirst, approver, key, score, fmt.Sprintf("special approver level %d", e.level(approver)), fmt.Sprintf("second confirmation required by %d", rec.special.ExpiresAt))
	e.commit(at)
	return false, nil
}
