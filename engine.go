package gradeaudit

import "sort"

func NewEngine(cfg Config, levels map[ActorID]int) (*Engine, error) {
	if cfg.ReviewWindow < 0 || cfg.ApprovalLimit < 0 || cfg.SpecialConfirmTTL < 0 {
		return nil, errorf(ErrInvalid, "time windows must not be negative")
	}
	if cfg.MinScore > cfg.MaxScore || cfg.MaxScoreDelta < 0 {
		return nil, errorf(ErrInvalid, "score configuration is invalid")
	}
	if cfg.Teachers == nil {
		return nil, errorf(ErrInvalid, "teachers are required")
	}
	clippedLevels := make(map[ActorID]int, len(levels))
	for actor, level := range levels {
		clippedLevels[actor] = level
	}
	return &Engine{
		cfg:       cfg,
		levels:    clippedLevels,
		records:   make(map[RecordKey]*Record),
		byStudent: make(map[studentSemesterKey]map[RecordKey]*Record),
		locked:    make(map[SemesterID]int64),
	}, nil
}

func (e *Engine) begin(at int64) error {
	if at < e.clock {
		return errorf(ErrClock, "time %d is before clock %d", at, e.clock)
	}
	return nil
}

func (e *Engine) commit(at int64) {
	if at > e.clock {
		e.clock = at
	}
}

func (e *Engine) teacher(course CourseID) (ActorID, bool) {
	teacher, ok := e.cfg.Teachers[course]
	return teacher, ok
}

func (e *Engine) level(actor ActorID) int {
	return e.levels[actor]
}

func (e *Engine) appendAudit(at int64, kind AuditKind, actor ActorID, key RecordKey, score int, reason, detail string) {
	e.auditSeq++
	e.audit = append(e.audit, AuditEntry{
		Seq:    e.auditSeq,
		At:     at,
		Kind:   kind,
		Actor:  actor,
		Record: key,
		Score:  score,
		Reason: reason,
		Detail: detail,
	})
}

func (e *Engine) indexRecord(rec *Record) {
	key := studentSemesterKey{student: rec.Key.Student, semester: rec.Key.Semester}
	if e.byStudent[key] == nil {
		e.byStudent[key] = make(map[RecordKey]*Record)
	}
	e.byStudent[key][rec.Key] = rec
}

func sortedRecords(records map[RecordKey]*Record) []*Record {
	out := make([]*Record, 0, len(records))
	for _, rec := range records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Semester != out[j].Key.Semester {
			return out[i].Key.Semester < out[j].Key.Semester
		}
		if out[i].Key.Course != out[j].Key.Course {
			return out[i].Key.Course < out[j].Key.Course
		}
		return out[i].Key.Student < out[j].Key.Student
	})
	return out
}
