package enrollment

type Snapshot struct {
	Status  Status
	Major   string
	OpenApp bool
}

type AuditRecord struct {
	At        int64
	StudentID string
	Action    string
	Detail    string
	Accepted  bool
}

// SnapshotAt returns the state, major and open-application flag the student
// had at tick. Historical answers are computed from the immutable version
// chain and application history, so later operations never change them.
// Cost is local to the student: O(log versions + applications).
func (e *Engine) SnapshotAt(studentID string, tick int64) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tick < 0 {
		return Snapshot{}, newErr(ErrInvalidArgument, "negative tick %d", tick)
	}
	s, ok := e.students[studentID]
	if !ok {
		return Snapshot{}, newErr(ErrNotFound, "student %q", studentID)
	}
	v := s.stateAt(tick)
	if len(s.versions) == 0 || tick < s.versions[0].EffectiveAt {
		return Snapshot{}, newErr(ErrNotFound, "student %q not enrolled at %d", studentID, tick)
	}
	open := false
	for _, a := range s.appHist {
		if a.submittedAt <= tick &&
			tick <= a.submittedAt+e.cfg.AppDeadline &&
			(!a.closed || a.closedAt > tick) {
			open = true
			break
		}
	}
	return Snapshot{Status: v.Status, Major: v.Major, OpenApp: open}, nil
}

// AuditLog returns a defensive copy of accepted operations only.
func (e *Engine) AuditLog() []AuditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditRecord, len(e.audit))
	copy(out, e.audit)
	return out
}
