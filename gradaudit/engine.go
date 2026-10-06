package gradaudit

import "sync"

// Engine is the concurrency-safe facade over all mutable state.
type Engine struct {
	mu          sync.RWMutex
	courses     map[string]*Course
	plans       map[string]*PlanVersion
	planOrder   map[string]int // version order; greater = newer
	students    map[string]*studentState
	recordSeq   int
	transferSeq map[string]int
}

type studentState struct {
	mu        sync.Mutex
	student   *Student
	records   map[string]*Record
	subs      []Substitution
	recordReg int
}

// NewEngine creates an empty engine.
func NewEngine() *Engine {
	return &Engine{
		courses:     map[string]*Course{},
		plans:       map[string]*PlanVersion{},
		planOrder:   map[string]int{},
		students:    map[string]*studentState{},
		transferSeq: map[string]int{},
	}
}

// AddCourse registers a catalog course.
func (e *Engine) AddCourse(c Course) *OpError {
	if c.Code == "" || c.Credit < 0 {
		return opError(ErrInvalid, "bad course %q", c.Code)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.courses[c.Code] = &c
	return nil
}

// AddPlan registers an immutable plan version; order defines newness.
func (e *Engine) AddPlan(p *PlanVersion, order int) *OpError {
	if p == nil || p.ID == "" || p.Root == nil {
		return opError(ErrInvalid, "bad plan")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plans[p.ID] = p
	e.planOrder[p.ID] = order
	return nil
}

// Enroll binds a student to the plan version in effect at entry.
func (e *Engine) Enroll(id, planID string) *OpError {
	if id == "" || planID == "" {
		return opError(ErrInvalid, "empty student or plan id")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.plans[planID]; !ok {
		return opError(ErrNotFound, "plan version %q", planID)
	}
	if _, exists := e.students[id]; exists {
		return opError(ErrInvalid, "student %q already enrolled", id)
	}
	e.students[id] = &studentState{
		student: &Student{ID: id, PlanID: planID},
		records: map[string]*Record{},
	}
	return nil
}

// RegisterRecordInput parameters for recording an enrollment.
type RegisterRecordInput struct {
	StudentID string
	RecordID  string
	Course    string
	Semester  int
	Score     float64
	Transfer  bool
}

// RegisterRecord records a normal or transferred enrollment.
func (e *Engine) RegisterRecord(in RegisterRecordInput) *OpError {
	if in.StudentID == "" || in.RecordID == "" || in.Course == "" || in.Semester < 0 {
		return opError(ErrInvalid, "bad record input %+v", in)
	}
	e.mu.RLock()
	st, ok := e.students[in.StudentID]
	course, cok := e.courses[in.Course]
	var plan *PlanVersion
	if ok {
		plan = e.plans[st.student.PlanID]
	}
	e.mu.RUnlock()
	if !ok {
		return opError(ErrNotFound, "student %q", in.StudentID)
	}
	if !cok {
		return opError(ErrNotFound, "course %q", in.Course)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if plan == nil {
		return opError(ErrNotFound, "plan version %q", st.student.PlanID)
	}
	if _, dup := st.records[in.RecordID]; dup {
		return opError(ErrInvalid, "record id %q duplicated", in.RecordID)
	}
	if in.Transfer {
		total := 0.0
		for _, r := range st.records {
			if r.Transfer && !r.Revoked && r.Score >= plan.PassScore {
				total += r.Credit
			}
		}
		// Acceptance against the cap is decided on the passing prefix; only a
		// record that would exceed the cap is rejected. Failures never consume
		// capacity and become relevant only if a later attempt passes.
		if in.Score >= plan.PassScore && total+course.Credit > plan.TransferCap+1e-9 {
			return opError(ErrTransferOverflow, "transfer cap %.4f exceeded (sum %.4f + %.4f)", plan.TransferCap, total, course.Credit)
		}
	}
	st.recordReg++
	rec := &Record{
		ID:       in.RecordID,
		Course:   in.Course,
		Semester: in.Semester,
		Score:    in.Score,
		Credit:   course.Credit,
		Transfer: in.Transfer,
		regOrder: st.recordReg,
	}
	if in.Transfer {
		e.mu.Lock()
		rec.transferOrder = e.transferSeq[in.StudentID]
		e.transferSeq[in.StudentID]++
		e.mu.Unlock()
	}
	st.records[in.RecordID] = rec
	return nil
}

// RevokeRecord marks a record revoked; a revoked record never counts.
func (e *Engine) RevokeRecord(studentID, recordID string) *OpError {
	if studentID == "" || recordID == "" {
		return opError(ErrInvalid, "empty id")
	}
	e.mu.RLock()
	st, ok := e.students[studentID]
	e.mu.RUnlock()
	if !ok {
		return opError(ErrNotFound, "student %q", studentID)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[recordID]
	if !ok {
		return opError(ErrNotFound, "record %q", recordID)
	}
	if rec.Revoked {
		return opError(ErrAlreadyRevoked, "record %q", recordID)
	}
	rec.Revoked = true
	return nil
}

// RegisterSubstitution records an approved substitution for a student.
func (e *Engine) RegisterSubstitution(studentID, from, to, planID string, effective int) *OpError {
	if studentID == "" || from == "" || to == "" || planID == "" || from == to || effective < 0 {
		return opError(ErrInvalid, "bad substitution input")
	}
	e.mu.RLock()
	st, ok := e.students[studentID]
	_, fc := e.courses[from]
	_, tc := e.courses[to]
	_, pc := e.plans[planID]
	e.mu.RUnlock()
	if !ok {
		return opError(ErrNotFound, "student %q", studentID)
	}
	if !fc || !tc {
		return opError(ErrNotFound, "course %q or %q", from, to)
	}
	if !pc {
		return opError(ErrNotFound, "plan version %q", planID)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.student.PlanID != planID {
		return opError(ErrSubNotApplicable, "substitution plan %q != bound plan %q", planID, st.student.PlanID)
	}
	st.subs = append(st.subs, Substitution{From: from, To: to, PlanID: planID, Effective: effective})
	return nil
}

// Migrate switches a student to a newer plan version after explicit request.
func (e *Engine) Migrate(studentID, planID string) *OpError {
	if studentID == "" || planID == "" {
		return opError(ErrInvalid, "empty id")
	}
	e.mu.RLock()
	st, ok := e.students[studentID]
	tp, pc := e.plans[planID]
	e.mu.RUnlock()
	if !ok {
		return opError(ErrNotFound, "student %q", studentID)
	}
	if !pc {
		return opError(ErrNotFound, "plan version %q", planID)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	cur := e.plans[st.student.PlanID]
	if e.planOrder[planID] < e.planOrder[cur.ID] {
		return opError(ErrVersionTooOld, "target %q older than current %q", planID, cur.ID)
	}
	if st.student.PlanID == planID {
		return nil
	}
	_ = tp
	st.student.PlanID = planID
	return nil
}

// Audit runs the graduation audit for one student.
func (e *Engine) Audit(studentID string) (*AuditResult, *OpError) {
	if studentID == "" {
		return nil, opError(ErrInvalid, "empty student id")
	}
	e.mu.RLock()
	st, ok := e.students[studentID]
	if !ok {
		e.mu.RUnlock()
		return nil, opError(ErrNotFound, "student %q", studentID)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	plan := e.plans[st.student.PlanID]
	courses := e.courses
	e.mu.RUnlock()
	if plan == nil {
		return nil, opError(ErrNotFound, "plan version %q", st.student.PlanID)
	}
	view := buildCountedView(plan, st, courses)
	return runAudit(studentID, st, plan, view), nil
}
