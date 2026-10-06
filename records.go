package gradeaudit

import "sort"

func validKey(key RecordKey) bool {
	return key.Student != "" && key.Course != "" && key.Semester != ""
}

func (e *Engine) isLocked(semester SemesterID) bool {
	_, ok := e.locked[semester]
	return ok
}

func (e *Engine) EnterInitialScore(at int64, actor ActorID, key RecordKey, score int) error {
	if at < 0 || actor == "" || !validKey(key) {
		return errorf(ErrInvalid, "initial score requires a time, actor and complete record key")
	}
	if score < e.cfg.MinScore || score > e.cfg.MaxScore {
		return errorf(ErrScore, "initial score %d is outside [%d,%d]", score, e.cfg.MinScore, e.cfg.MaxScore)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.begin(at); err != nil {
		return err
	}
	if _, exists := e.records[key]; exists {
		return errorf(ErrState, "record %s/%s/%s already exists", key.Student, key.Course, key.Semester)
	}
	teacher, ok := e.teacher(key.Course)
	if !ok || actor != teacher {
		return errorf(ErrPermission, "actor %s is not teacher of course %s", actor, key.Course)
	}
	if e.isLocked(key.Semester) {
		return errorf(ErrLocked, "semester %s is locked", key.Semester)
	}

	rec := &Record{
		Key:       key,
		InitialAt: at,
		Versions: []Version{{
			Score:  score,
			At:     at,
			Source: SourceInitial,
		}},
	}
	e.records[key] = rec
	e.indexRecord(rec)
	e.appendAudit(at, AuditInitial, actor, key, score, "initial score entered", "score within configured range")
	e.commit(at)
	return nil
}

func versionAt(versions []Version, at int64) (Version, bool) {
	idx := sort.Search(len(versions), func(i int) bool {
		return versions[i].At > at
	}) - 1
	if idx < 0 {
		return Version{}, false
	}
	return versions[idx], true
}

func underReviewAt(spans []reviewSpan, at int64) bool {
	idx := sort.Search(len(spans), func(i int) bool {
		return spans[i].Start > at
	}) - 1
	return idx >= 0 && spans[idx].Start <= at && (spans[idx].End == 0 || spans[idx].End > at)
}

func (e *Engine) snapshotLocked(rec *Record, at int64) Snapshot {
	snapshot := Snapshot{
		Record:      rec.Key,
		At:          at,
		UnderReview: underReviewAt(rec.spans, at),
	}
	if version, ok := versionAt(rec.Versions, at); ok {
		snapshot.HasScore = true
		snapshot.Score = version.Score
		snapshot.Source = version.Source
	}
	return snapshot
}

func (e *Engine) validateQuery(at int64, now int64, key RecordKey) error {
	if at < 0 || now < 0 || now < at || !validKey(key) {
		return errorf(ErrInvalid, "query requires 0 <= at <= now and a complete key")
	}
	if now < e.clock {
		return errorf(ErrClock, "query time %d is before clock %d", now, e.clock)
	}
	return nil
}

func (e *Engine) SnapshotAt(key RecordKey, at int64, now int64) (Snapshot, error) {
	if at < 0 || now < 0 || now < at || !validKey(key) {
		return Snapshot{Record: key, At: at}, errorf(ErrInvalid, "query requires 0 <= at <= now and a complete key")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return Snapshot{Record: key, At: at}, errorf(ErrClock, "query time %d is before clock %d", now, e.clock)
	}
	rec := e.records[key]
	if rec == nil {
		return Snapshot{Record: key, At: at}, errorf(ErrNotFound, "record %s/%s/%s does not exist", key.Student, key.Course, key.Semester)
	}
	e.sweepRecordLocked(rec, now)
	e.commit(now)
	return e.snapshotLocked(rec, at), nil
}

func (e *Engine) SemesterAverageAt(student StudentID, semester SemesterID, at int64, now int64) (float64, bool, error) {
	if at < 0 || now < 0 || now < at || student == "" || semester == "" {
		return 0, false, errorf(ErrInvalid, "average query requires a student, semester and 0 <= at <= now")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return 0, false, errorf(ErrClock, "query time %d is before clock %d", now, e.clock)
	}
	records := e.byStudent[studentSemesterKey{student: student, semester: semester}]
	for _, rec := range sortedRecords(records) {
		e.sweepRecordLocked(rec, now)
	}
	e.commit(now)

	sum := 0
	count := 0
	for _, rec := range sortedRecords(records) {
		if version, ok := versionAt(rec.Versions, at); ok {
			sum += version.Score
			count++
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	return float64(sum) / float64(count), true, nil
}
