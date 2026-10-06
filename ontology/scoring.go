package ontology

// snapUp rounds an exact average num/den upward toward the maximum score
// onto the question's step grid: result is the smallest grid point
// (multiple of Step) that is >= num/den, clamped to MaxScore.
func snapUp(q Question, num, den int) int {
	step := q.Step
	idx := (num + den*step - 1) / (den * step) // ceil(avg/step)
	if idx*step > q.MaxScore {
		return q.MaxScore
	}
	return idx * step
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// finalizeInitialLocked either agrees (average, snapped up) or opens an
// arbitration task staffed from a third group.
func (e *Engine) finalizeInitialLocked(s *sheetState, a, b *task) {
	q := e.questions[s.question]
	sa, sb := *a.score, *b.score
	gap := absInt(sa - sb)
	if gap <= q.Threshold {
		fin := snapUp(q, sa+sb, 2)
		s.final = &fin
		highSide := a.reviewerID
		if sb > sa {
			highSide = b.reviewerID
		}
		basis := "agreement;gap=" + itoa(gap) + ";average_grid_up;higher_side=" + highSide
		e.recordLocked(EventFinal, s.id, "", "", "", &fin, basis)
		return
	}
	t := &task{id: e.nextTaskIDLocked(), sheetID: s.id, role: "arbitrator"}
	s.tasks = append(s.tasks, t)
	req := e.requirementsForLocked(s, t)
	id, reasons := e.chooseLocked(req)
	basis := "gap=" + itoa(gap) + ">threshold=" + itoa(q.Threshold)
	if id == "" {
		e.queueTaskLocked(s, t)
		basis += ";no_candidate_pending"
	} else {
		e.assignTaskLocked(s, t, id, reasons)
		basis += ";arbitrator=" + id
	}
	e.recordLocked(EventArbitration, s.id, t.id, id, "arbitrator", nil, basis)
}

// finalizeArbitrationLocked applies the closer-side rule; an arbitrator far
// from both initial scores becomes the final score directly.
func (e *Engine) finalizeArbitrationLocked(s *sheetState, arb *task) {
	q := e.questions[s.question]
	var ini []*task
	for _, t := range s.tasks {
		if t.role == "initial" && !t.withdrawn && t.score != nil {
			ini = append(ini, t)
		}
	}
	a, b := ini[0], ini[1]
	sa, sb, sc := *a.score, *b.score, *arb.score
	da, db := absInt(sc-sa), absInt(sc-sb)
	if da > q.Threshold && db > q.Threshold {
		fin := sc
		s.final = &fin
		basis := "arbitrator_far_from_both;d_a=" + itoa(da) + ";d_b=" + itoa(db)
		e.recordLocked(EventFinal, s.id, arb.id, arb.reviewerID, "arbitrator", &fin, basis)
		return
	}
	chosen := a
	if db < da || (db == da && sb > sa) {
		chosen = b
	}
	fin := snapUp(q, sc+*chosen.score, 2)
	s.final = &fin
	basis := "closer_side=" + chosen.reviewerID +
		";d_a=" + itoa(da) + ";d_b=" + itoa(db) + ";average_grid_up"
	e.recordLocked(EventFinal, s.id, arb.id, arb.reviewerID, "arbitrator", &fin, basis)
}

func (e *Engine) findTaskLocked(taskID string) (*sheetState, *task) {
	for _, sid := range e.sheetID {
		s := e.sheets[sid]
		for _, t := range s.tasks {
			if t.id == taskID {
				return s, t
			}
		}
	}
	return nil, nil
}

func (e *Engine) submitLocked(taskID string, score int) error {
	if taskID == "" {
		return mkErr(ErrInvalidArgument, "empty task id")
	}
	s, t := e.findTaskLocked(taskID)
	if s == nil {
		return mkErr(ErrNotFound, "unknown task: "+taskID)
	}
	if t.reviewerID == "" {
		return mkErr(ErrTaskState, "task pending assignment: "+taskID)
	}
	r := e.reviewers[t.reviewerID]
	if !r.active {
		return mkErr(ErrDeactivated, "reviewer deactivated: "+r.id)
	}
	if s.final != nil {
		return mkErr(ErrTaskState, "sheet already finalized: "+s.id)
	}
	if t.withdrawn {
		return mkErr(ErrTaskState, "task withdrawn: "+taskID)
	}
	if t.score != nil {
		return mkErr(ErrTaskState, "duplicate submission: "+taskID)
	}
	q := e.questions[s.question]
	if score < 0 || score > q.MaxScore || score%q.Step != 0 {
		return mkErr(ErrScore, "score off grid or out of range")
	}

	t.score = intp(score)
	r.load--
	e.recordLocked(EventSubmit, s.id, t.id, r.id, t.role, t.score, "on_grid")

	if t.role == "arbitrator" {
		e.finalizeArbitrationLocked(s, t)
	} else {
		var scoredIni []*task
		for _, ot := range s.tasks {
			if ot.role == "initial" && !ot.withdrawn && ot.score != nil {
				scoredIni = append(scoredIni, ot)
			}
		}
		if len(scoredIni) == 2 {
			e.finalizeInitialLocked(s, scoredIni[0], scoredIni[1])
		}
	}
	e.assignPendingLocked()
	return nil
}

func (e *Engine) withdrawLocked(taskID string) error {
	if taskID == "" {
		return mkErr(ErrInvalidArgument, "empty task id")
	}
	s, t := e.findTaskLocked(taskID)
	if s == nil {
		return mkErr(ErrNotFound, "unknown task: "+taskID)
	}
	if t.reviewerID == "" {
		return mkErr(ErrTaskState, "task pending assignment: "+taskID)
	}
	r := e.reviewers[t.reviewerID]
	if !r.active {
		return mkErr(ErrDeactivated, "reviewer deactivated: "+r.id)
	}
	if s.final != nil {
		return mkErr(ErrTaskState, "sheet already finalized: "+s.id)
	}
	if t.withdrawn {
		return mkErr(ErrTaskState, "task already withdrawn: "+taskID)
	}
	if t.score != nil {
		return mkErr(ErrTaskState, "task already submitted: "+taskID)
	}

	reviewerID := r.id
	r.load--
	t.withdrawn = true
	t.reviewerID = ""
	e.recordLocked(EventWithdraw, s.id, taskID, reviewerID, t.role, nil,
		"pre_submission;reviewer_banned_from_reselection")
	e.replaceLocked(s, t.role, reviewerID)
	e.assignPendingLocked()
	return nil
}

func (e *Engine) deactivateLocked(reviewerID string) error {
	if reviewerID == "" {
		return mkErr(ErrInvalidArgument, "empty reviewer id")
	}
	r, ok := e.reviewers[reviewerID]
	if !ok {
		return mkErr(ErrNotFound, "unknown reviewer: "+reviewerID)
	}
	if !r.active {
		return mkErr(ErrDeactivated, "reviewer already deactivated: "+reviewerID)
	}
	r.active = false

	type heldTask struct {
		s *sheetState
		t *task
	}
	var held []heldTask
	for _, sid := range e.sheetID {
		s := e.sheets[sid]
		if s.final != nil {
			continue
		}
		for _, t := range s.tasks {
			if t.reviewerID == reviewerID && !t.withdrawn && t.score == nil {
				held = append(held, heldTask{s, t})
			}
		}
	}
	for _, h := range held {
		r.load--
		h.t.withdrawn = true
		h.t.reviewerID = ""
		e.recordLocked(EventWithdraw, h.s.id, h.t.id, reviewerID, h.t.role, nil,
			"reviewer_deactivated;reviewer_banned_from_reselection")
	}
	for _, h := range held {
		e.replaceLocked(h.s, h.t.role, reviewerID)
	}
	e.assignPendingLocked()
	return nil
}

// recordLocked appends an immutable audit entry with the next sequence id.
func (e *Engine) recordLocked(kind EventKind, sheet, taskID, reviewer, role string, score *int, basis string) {
	e.seq++
	e.events = append(e.events, Event{
		Seq:      e.seq,
		Kind:     kind,
		Sheet:    sheet,
		TaskID:   taskID,
		Reviewer: reviewer,
		Role:     role,
		Score:    score,
		Basis:    basis,
	})
}

func (e *Engine) snapshotLocked() View {
	v := View{
		Reviewers: map[string]Reviewer{},
		Questions: map[string]Question{},
		Groups:    map[string]Group{},
		Sheets:    make([]SheetView, 0, len(e.sheetID)),
		Events:    make([]Event, len(e.events)),
	}
	for id := range e.groups {
		v.Groups[id] = Group{ID: id}
	}
	for id, q := range e.questions {
		v.Questions[id] = q
	}
	for _, id := range e.revIDs {
		r := e.reviewers[id]
		v.Reviewers[id] = Reviewer{ID: r.id, GroupID: r.groupID, Quota: r.quota, Active: r.active}
	}
	for _, sid := range e.sheetID {
		s := e.sheets[sid]
		sv := SheetView{
			ID:          s.id,
			Question:    s.question,
			Student:     s.student,
			FinalScore:  copyInt(s.final),
			PendingTask: s.pending != nil,
			Tasks:       make([]TaskView, 0, len(s.tasks)),
		}
		for _, t := range s.tasks {
			sv.Tasks = append(sv.Tasks, TaskView{
				ID:         t.id,
				SheetID:    t.sheetID,
				ReviewerID: t.reviewerID,
				Role:       t.role,
				Score:      copyInt(t.score),
				Withdrawn:  t.withdrawn,
			})
		}
		v.Sheets = append(v.Sheets, sv)
	}
	copy(v.Events, e.events)
	return v
}

func copyInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
