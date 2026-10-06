package gradeaudit

import "fmt"

func (e *Engine) ApplyReview(at int64, student ActorID, key RecordKey) error {
	if at < 0 || student == "" || !validKey(key) {
		return errorf(ErrInvalid, "review application requires a time, student and complete key")
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
		return errorf(ErrLocked, "semester %s is locked", key.Semester)
	}
	if ActorID(key.Student) != student {
		return errorf(ErrPermission, "only student %s may apply for this record", key.Student)
	}
	if e.sweepRecordLocked(rec, at) {
		e.commit(at)
		return errorf(ErrTimeout, "pending item expired during this touch")
	}
	deadline := rec.InitialAt + e.cfg.ReviewWindow
	if at > deadline {
		return errorf(ErrTimeout, "review window closed at %d", deadline)
	}
	if rec.app != nil && (rec.app.State == appOpen || rec.app.State == appPending) {
		return errorf(ErrState, "record already has an open application")
	}

	rec.app = &Application{OpenedAt: at, State: appOpen}
	rec.spans = append(rec.spans, reviewSpan{Start: at})
	e.appendAudit(at, AuditApplication, student, key, 0, fmt.Sprintf("within review window ending at %d", deadline), "application accepted")
	e.commit(at)
	return nil
}

func (e *Engine) RejectApplication(at int64, actor ActorID, key RecordKey, reason string) error {
	if at < 0 || actor == "" || !validKey(key) {
		return errorf(ErrInvalid, "application rejection requires a time, actor and complete key")
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
		return errorf(ErrLocked, "semester %s is locked", key.Semester)
	}
	teacher, ok := e.teacher(key.Course)
	if !ok || actor != teacher {
		return errorf(ErrPermission, "only course teacher may reject this application")
	}
	if e.sweepRecordLocked(rec, at) {
		e.commit(at)
		return errorf(ErrTimeout, "pending item expired during this touch")
	}
	if rec.app == nil || rec.app.State != appOpen {
		return errorf(ErrState, "no accepted application is waiting for a decision")
	}

	rec.app.State = appRejected
	if len(rec.spans) > 0 {
		rec.spans[len(rec.spans)-1].End = at
	}
	e.appendAudit(at, AuditApplicationReject, actor, key, 0, reason, "application closed without a score version")
	e.commit(at)
	return nil
}
