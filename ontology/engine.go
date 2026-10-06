// Package ontology implements a deterministic grading task allocation
// and double-marking arbitration engine.
package ontology

import (
	"sort"
	"sync"
)

type reviewerState struct {
	id      string
	groupID string
	quota   int
	active  bool
	load    int // number of currently held, unfinished (non-withdrawn, unscored) tasks
	avoids  map[string]bool
}

// task is one marking assignment. Withdrawn tasks are retained as audit
// evidence but carry no valid score.
type task struct {
	id         string
	sheetID    string
	reviewerID string
	role       string
	score      *int
	withdrawn  bool
}

// sheetState is the internal mutable state of one sheet.
type sheetState struct {
	id       string
	question string
	student  string
	tasks    []*task
	final    *int
	pending  *task // currently unassignable task, if any
}

// Engine is the allocation and arbitration engine. All entry points take
// the single mutex, so concurrent calls are equivalent to some serial
// interleaving.
type Engine struct {
	mu sync.Mutex

	groups    map[string]bool
	questions map[string]Question
	reviewers map[string]*reviewerState
	revIDs    []string // sorted, for deterministic scans

	sheets  map[string]*sheetState
	sheetID []string // insertion order, for deterministic pending processing

	// pairUsed records that a reviewer has already appeared for a student,
	// across any question. Keyed reviewerID + "\x00" + student.
	pairUsed map[string]bool

	pending []*task // tasks waiting for a feasible reviewer, FIFO

	events      []Event
	seq         int
	taskCounter int
	chooseScans int // reviewer records examined by the most recent chooseLocked
}

// New creates an empty engine.
func New() *Engine {
	return &Engine{
		groups:    map[string]bool{},
		questions: map[string]Question{},
		reviewers: map[string]*reviewerState{},
		sheets:    map[string]*sheetState{},
		pairUsed:  map[string]bool{},
	}
}

func pairKey(reviewer, student string) string { return reviewer + "\x00" + student }

func intp(v int) *int { return &v }

// set of students whose sheets that reviewer must never mark.
type ConfigInput struct {
	Groups    []Group
	Questions []Question
	Reviewers []Reviewer
	Avoid     map[string][]string
	Sheets    []Sheet
}

// AddGroup registers a marking group.
func (e *Engine) AddGroup(g Group) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addGroupLocked(g)
}

// AddQuestion registers a question.
func (e *Engine) AddQuestion(q Question) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addQuestionLocked(q)
}

// AddReviewer registers a reviewer with its avoidance relationships.
func (e *Engine) AddReviewer(r Reviewer, avoids []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.addReviewerLocked(r, avoids)
	if err == nil {
		e.assignPendingLocked()
	}
	return err
}

// AddSheet registers a sheet and immediately attempts its two initial
// assignments; unsatisfiable tasks become pending.
func (e *Engine) AddSheet(s Sheet) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addSheetLocked(s)
}

// TriggerAllocation retries every pending task. It is a no-op (and never an
// error) when nothing can be assigned.
func (e *Engine) TriggerAllocation() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.assignPendingLocked()
}

// Submit records a score for a task. Duplicate, withdrawn and finalized
// submissions are rejected without any state change.
func (e *Engine) Submit(taskID string, score int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.submitLocked(taskID, score)
}

// Withdraw releases an unsubmitted task; the sheet keeps any other valid
// initial score, and the withdrawn reviewer is banned from reselection.
func (e *Engine) Withdraw(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.withdrawLocked(taskID)
}

// Deactivate a reviewer: all unfinished tasks are treated as withdrawn and
// reassigned; submitted scores are retained.
func (e *Engine) Deactivate(reviewerID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deactivateLocked(reviewerID)
}

// Snapshot returns a deep copy of the observable engine state.
func (e *Engine) Snapshot() View {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

// chooseReq describes one reviewer selection.
type chooseReq struct {
	sheet    *sheetState
	role     string
	groupsOK map[string]bool // allowed group IDs
	banned   map[string]bool // reviewers forbidden for this sheet
}

// chooseLocked scans all reviewers once and selects:
//   - eligible: active, group allowed, quota not full, no avoidance for the
//     sheet's student, not paired with that student on any question, not
//     banned for this sheet;
//   - among eligible candidates: minimum current load, then minimum ID.
//
// The scan touches only reviewer records (a fixed set) and O(1) maps; it
// never walks completed tasks or other sheets. Cost is O(R) and is
// independent of the number of sheets and completed tasks.
func (e *Engine) chooseLocked(req chooseReq) (string, map[string]int) {
	reasons := map[string]int{}
	var bestID string
	bestLoad := 0
	tied := 0
	e.chooseScans = 0
	for _, id := range e.revIDs {
		e.chooseScans++
		r := e.reviewers[id]
		switch {
		case !r.active:
			reasons["deactivated"]++
		case !req.groupsOK[r.groupID]:
			reasons["group"]++
		case r.load >= r.quota:
			reasons["quota"]++
		case r.avoids[req.sheet.student]:
			reasons["avoid"]++
		case e.pairUsed[pairKey(id, req.sheet.student)]:
			reasons["paired"]++
		case req.banned[id]:
			reasons["banned"]++
		default:
			if bestID == "" {
				bestID, bestLoad = id, r.load
			} else if r.load < bestLoad {
				bestID, bestLoad, tied = id, r.load, 0
			} else if r.load == bestLoad {
				tied++
				if id < bestID {
					bestID = id
				}
			}
		}
	}
	if bestID == "" {
		return "", reasons
	}
	return bestID, map[string]int{"tied_at_min_load": tied, "selected_load": bestLoad}
}

// assignPendingLocked drains the FIFO pending queue. Assignments only raise
// loads, so a task feasible later stays feasible; one FIFO pass is enough.
func (e *Engine) assignPendingLocked() {
	i := 0
	for i < len(e.pending) {
		t := e.pending[i]
		s := e.sheets[t.sheetID]
		req := e.requirementsForLocked(s, t)
		id, reasons := e.chooseLocked(req)
		if id == "" {
			i++
			continue
		}
		e.pending = append(e.pending[:i], e.pending[i+1:]...)
		e.assignTaskLocked(s, t, id, reasons)
	}
}

// requirementsForLocked builds group and per-sheet banning constraints.
func (e *Engine) requirementsForLocked(s *sheetState, t *task) chooseReq {
	banned := map[string]bool{}
	for _, ot := range s.tasks {
		if ot == t || ot.withdrawn {
			continue
		}
		if ot.score != nil && ot.reviewerID != "" {
			banned[ot.reviewerID] = true // a reviewer marks a given sheet at most once
		}
	}
	groupsOK := map[string]bool{}
	if t.role == "arbitrator" {
		used := map[string]bool{}
		for _, ot := range s.tasks {
			if ot.role == "initial" && !ot.withdrawn && ot.reviewerID != "" {
				used[e.reviewers[ot.reviewerID].groupID] = true
			}
		}
		for g := range e.groups {
			if !used[g] {
				groupsOK[g] = true
			}
		}
	} else {
		otherGroup := ""
		for _, ot := range s.tasks {
			if ot != t && ot.role == "initial" && !ot.withdrawn && ot.reviewerID != "" {
				otherGroup = e.reviewers[ot.reviewerID].groupID
			}
		}
		for g := range e.groups {
			if g != otherGroup {
				groupsOK[g] = true
			}
		}
	}
	return chooseReq{sheet: s, role: t.role, groupsOK: groupsOK, banned: banned}
}

// assignTaskLocked binds a task to the chosen reviewer.
func (e *Engine) assignTaskLocked(s *sheetState, t *task, id string, tie map[string]int) {
	t.reviewerID = id
	t.withdrawn = false
	e.reviewers[id].load++
	e.pairUsed[pairKey(id, s.student)] = true
	if s.pending == t {
		s.pending = nil
	}
	basis := "min_load=" + itoa(tie["selected_load"]) +
		";tied_candidates=" + itoa(tie["tied_at_min_load"]) + ";tie_break=min_id"
	e.recordLocked(EventAssign, s.id, t.id, id, t.role, nil, basis)
}

// queueTaskLocked detaches a task to await a feasible reviewer.
func (e *Engine) queueTaskLocked(s *sheetState, t *task) {
	t.reviewerID = ""
	s.pending = t
	e.pending = append(e.pending, t)
}

// replaceLocked appends a fresh task of the same role and tries to assign it.
// withdrawnID is the reviewer who must never be re-picked for this sheet.
func (e *Engine) replaceLocked(s *sheetState, role, withdrawnID string) {
	t := &task{id: e.nextTaskIDLocked(), sheetID: s.id, role: role}
	s.tasks = append(s.tasks, t)
	req := e.requirementsForLocked(s, t)
	req.banned[withdrawnID] = true
	id, reasons := e.chooseLocked(req)
	if id == "" {
		e.queueTaskLocked(s, t)
		return
	}
	e.assignTaskLocked(s, t, id, reasons)
}

func (e *Engine) nextTaskIDLocked() string {
	e.taskCounter++
	return "task-" + itoa(e.taskCounter)
}

// ---- configuration -----------------------------------------------------

func (e *Engine) addGroupLocked(g Group) error {
	if g.ID == "" {
		return mkErr(ErrInvalidArgument, "empty group id")
	}
	if e.groups[g.ID] {
		return mkErr(ErrInvalidArgument, "duplicate group: "+g.ID)
	}
	e.groups[g.ID] = true
	return nil
}

func (e *Engine) addQuestionLocked(q Question) error {
	if q.ID == "" {
		return mkErr(ErrInvalidArgument, "empty question id")
	}
	if _, ok := e.questions[q.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate question: "+q.ID)
	}
	if q.MaxScore <= 0 || q.Step <= 0 || q.MaxScore%q.Step != 0 {
		return mkErr(ErrInvalidArgument, "max score must be a positive multiple of step")
	}
	if q.Threshold < 0 {
		return mkErr(ErrInvalidArgument, "negative threshold")
	}
	e.questions[q.ID] = q
	return nil
}

func (e *Engine) addReviewerLocked(r Reviewer, avoids []string) error {
	if r.ID == "" {
		return mkErr(ErrInvalidArgument, "empty reviewer id")
	}
	if _, ok := e.reviewers[r.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate reviewer: "+r.ID)
	}
	if !e.groups[r.GroupID] {
		return mkErr(ErrNotFound, "unknown group: "+r.GroupID)
	}
	if r.Quota < 0 {
		return mkErr(ErrInvalidArgument, "negative quota")
	}
	av := map[string]bool{}
	for _, st := range avoids {
		if st == "" {
			return mkErr(ErrInvalidArgument, "empty avoided student")
		}
		av[st] = true
	}
	e.reviewers[r.ID] = &reviewerState{
		id: r.ID, groupID: r.GroupID, quota: r.Quota, active: r.Active, avoids: av,
	}
	e.revIDs = append(e.revIDs, r.ID)
	sort.Strings(e.revIDs)
	return nil
}

func (e *Engine) addSheetLocked(s Sheet) error {
	if s.ID == "" {
		return mkErr(ErrInvalidArgument, "empty sheet id")
	}
	if _, ok := e.sheets[s.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate sheet: "+s.ID)
	}
	if _, ok := e.questions[s.Question]; !ok {
		return mkErr(ErrNotFound, "unknown question: "+s.Question)
	}
	if s.Student == "" {
		return mkErr(ErrInvalidArgument, "empty student")
	}
	st := &sheetState{id: s.ID, question: s.Question, student: s.Student}
	e.sheets[s.ID] = st
	e.sheetID = append(e.sheetID, s.ID)
	for range [2]struct{}{} {
		t := &task{id: e.nextTaskIDLocked(), sheetID: st.id, role: "initial"}
		st.tasks = append(st.tasks, t)
		req := e.requirementsForLocked(st, t)
		id, reasons := e.chooseLocked(req)
		if id == "" {
			e.queueTaskLocked(st, t)
		} else {
			e.assignTaskLocked(st, t, id, reasons)
		}
	}
	e.assignPendingLocked()
	return nil
}

// itoa formats a non-negative small integer without strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
