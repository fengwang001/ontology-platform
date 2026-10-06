package enrollment

import (
	"sort"
	"sync"
)

// Stats counts primitive probes so tests can verify the complexity
// guarantees of the time-conflict and capacity checks.
type Stats struct {
	// SlotProbes counts map lookups performed by time-conflict checks.
	SlotProbes int64
	// CapacityProbes counts ledger reads performed by capacity checks.
	CapacityProbes int64
}

// Engine holds all enrollment state. Every public operation takes the
// same mutex, so concurrent calls are equivalent to some serial order.
type Engine struct {
	mu  sync.Mutex
	cfg Config

	courses  map[CourseID]Course
	sections map[SectionID]*Section

	prereq map[CourseID]map[CourseID]bool // prereq[c] = direct prerequisites of c
	coreq  map[CourseID]map[CourseID]bool // symmetric
	mutex  map[CourseID]map[CourseID]bool // symmetric

	records  map[StudentID]map[CourseID]int       // past-term grades
	enrolled map[StudentID]map[CourseID]SectionID // current-term selections
	occupied map[StudentID]map[Slot]SectionID     // current-term slot index
	credits  map[StudentID]int                    // current-term credit sum
	ledger   map[SectionID]int                    // seats taken per section

	stats Stats
}

// NewEngine returns an empty engine with the given policy config.
func NewEngine(cfg Config) *Engine {
	return &Engine{
		cfg:      cfg,
		courses:  make(map[CourseID]Course),
		sections: make(map[SectionID]*Section),
		prereq:   make(map[CourseID]map[CourseID]bool),
		coreq:    make(map[CourseID]map[CourseID]bool),
		mutex:    make(map[CourseID]map[CourseID]bool),
		records:  make(map[StudentID]map[CourseID]int),
		enrolled: make(map[StudentID]map[CourseID]SectionID),
		occupied: make(map[StudentID]map[Slot]SectionID),
		credits:  make(map[StudentID]int),
		ledger:   make(map[SectionID]int),
	}
}

// --- catalog administration ---

// AddCourse registers a course.
func (e *Engine) AddCourse(id CourseID, credits int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.courses[id] = Course{ID: id, Credits: credits}
}

// AddSection registers a teaching section of a course.
func (e *Engine) AddSection(id SectionID, course CourseID, capacity int, slots ...Slot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := make(map[Slot]bool, len(slots))
	for _, s := range slots {
		set[s] = true
	}
	e.sections[id] = &Section{ID: id, Course: course, Capacity: capacity, Slots: set}
}

// AddPrereq makes p a direct prerequisite of c.
func (e *Engine) AddPrereq(c, p CourseID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	put(e.prereq, c, p)
}

// AddCoreq makes a and b mutual corequisites.
func (e *Engine) AddCoreq(a, b CourseID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	put(e.coreq, a, b)
	put(e.coreq, b, a)
}

// AddMutex makes a and b mutually exclusive.
func (e *Engine) AddMutex(a, b CourseID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	put(e.mutex, a, b)
	put(e.mutex, b, a)
}

func put(rel map[CourseID]map[CourseID]bool, from, to CourseID) {
	if rel[from] == nil {
		rel[from] = make(map[CourseID]bool)
	}
	rel[from][to] = true
}

// SetRecord records a past-term grade for a student.
func (e *Engine) SetRecord(s StudentID, c CourseID, grade int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.records[s] == nil {
		e.records[s] = make(map[CourseID]int)
	}
	e.records[s][c] = grade
}

// SetCapacity adjusts a section's capacity mid-term. Lowering below the
// current enrollment keeps everyone enrolled but rejects new picks until
// drops bring the count back below the capacity.
func (e *Engine) SetCapacity(id SectionID, capacity int) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if capacity < 0 {
		return &Error{Code: CodeInvalidParam, Section: id, Detail: "negative capacity"}
	}
	sec, ok := e.sections[id]
	if !ok {
		return &Error{Code: CodeNotFound, Section: id, Detail: "section not found"}
	}
	sec.Capacity = capacity
	return nil
}

// --- queries ---

// Enrolled returns a copy of the student's current-term selections.
func (e *Engine) Enrolled(s StudentID) map[CourseID]SectionID {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[CourseID]SectionID, len(e.enrolled[s]))
	for c, sec := range e.enrolled[s] {
		out[c] = sec
	}
	return out
}

// Count returns the number of seats taken in a section.
func (e *Engine) Count(id SectionID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ledger[id]
}

// Stats returns the probe counters.
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// --- operations ---

// BatchEnroll validates picks in submission order, each against the
// hypothetical state where the earlier picks of the batch are already
// selected. All-or-nothing: the first failing pick rejects the whole
// batch and nothing changes.
func (e *Engine) BatchEnroll(s StudentID, picks []Pick) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(picks) == 0 {
		return &Error{Code: CodeInvalidParam, Student: s, Detail: "empty batch"}
	}
	seen := make(map[CourseID]bool, len(picks))
	for _, p := range picks {
		if seen[p.Course] {
			return &Error{Code: CodeInvalidParam, Student: s, Course: p.Course,
				Detail: "course appears twice in batch"}
		}
		seen[p.Course] = true
	}

	v := e.newView(s)
	v.batch = seen
	secs := make([]*Section, 0, len(picks))
	for _, p := range picks {
		sec, err := e.resolve(s, p)
		if err != nil {
			return err
		}
		if err := e.checkEnroll(s, v, p.Course, sec); err != nil {
			return err
		}
		v.add(e, sec)
		secs = append(secs, sec)
	}
	for _, sec := range secs {
		e.applyEnroll(s, sec)
	}
	return nil
}

// Drop removes a course and cascades: any selected course whose coreq
// support disappears is dropped too, recursively, releasing capacity.
// Drops are not subject to capacity or time constraints.
func (e *Engine) Drop(s StudentID, c CourseID) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.courses[c]; !ok {
		return &Error{Code: CodeNotFound, Student: s, Course: c, Detail: "course not found"}
	}
	if _, ok := e.enrolled[s][c]; !ok {
		return &Error{Code: CodeNotEnrolled, Student: s, Course: c, Detail: "course not selected"}
	}
	e.applyDrop(s, c)
	e.cascadeUnsupported(s)
	return nil
}

// Switch atomically replaces an enrolled section with another section
// (of the same or a different course). The new pick is judged against
// the state where the old section is already dropped; on any failure
// the old selection is kept intact.
func (e *Engine) Switch(s StudentID, oldSection, newSection SectionID) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if oldSection == newSection {
		return &Error{Code: CodeInvalidParam, Student: s, Section: oldSection,
			Detail: "switch to the same section"}
	}
	oldSec, ok := e.sections[oldSection]
	if !ok {
		return &Error{Code: CodeNotFound, Student: s, Section: oldSection, Detail: "old section not found"}
	}
	newSec, ok := e.sections[newSection]
	if !ok {
		return &Error{Code: CodeNotFound, Student: s, Section: newSection, Detail: "new section not found"}
	}
	if cur, ok := e.enrolled[s][oldSec.Course]; !ok || cur != oldSection {
		return &Error{Code: CodeNotEnrolled, Student: s, Course: oldSec.Course, Section: oldSection,
			Detail: "old section not selected"}
	}

	v := e.newView(s)
	v.remove(e, oldSec)
	if err := e.checkEnroll(s, v, newSec.Course, newSec); err != nil {
		return err
	}
	v.add(e, newSec)
	// The remaining selections must keep their coreq support: switching
	// away must not strand a coreq partner.
	for _, c := range sortedViewCourses(v) {
		for _, co := range sortedCourses(e.coreq[c]) {
			if _, ok := v.courses[co]; !ok {
				return &Error{Code: CodeCoreqUnmet, Student: s, Course: c,
					Detail: "coreq " + string(co) + " lost by switching away " + string(oldSec.Course)}
			}
		}
	}

	e.applyDrop(s, oldSec.Course)
	e.applyEnroll(s, newSec)
	return nil
}

// --- state transitions (lock held by callers) ---

func (e *Engine) applyEnroll(s StudentID, sec *Section) {
	if e.enrolled[s] == nil {
		e.enrolled[s] = make(map[CourseID]SectionID)
	}
	if e.occupied[s] == nil {
		e.occupied[s] = make(map[Slot]SectionID)
	}
	e.enrolled[s][sec.Course] = sec.ID
	for slot := range sec.Slots {
		e.occupied[s][slot] = sec.ID
	}
	e.credits[s] += e.courses[sec.Course].Credits
	e.ledger[sec.ID]++
}

func (e *Engine) applyDrop(s StudentID, c CourseID) {
	sec := e.sections[e.enrolled[s][c]]
	delete(e.enrolled[s], c)
	for slot := range sec.Slots {
		delete(e.occupied[s], slot)
	}
	e.credits[s] -= e.courses[c].Credits
	e.ledger[sec.ID]--
}

// cascadeUnsupported repeatedly drops selected courses that lost a
// coreq partner, until a fixpoint is reached.
func (e *Engine) cascadeUnsupported(s StudentID) {
	for {
		var doomed []CourseID
		for c := range e.enrolled[s] {
			for co := range e.coreq[c] {
				if _, ok := e.enrolled[s][co]; !ok {
					doomed = append(doomed, c)
					break
				}
			}
		}
		if len(doomed) == 0 {
			return
		}
		sort.Slice(doomed, func(i, j int) bool { return doomed[i] < doomed[j] })
		for _, c := range doomed {
			e.applyDrop(s, c)
		}
	}
}

func sortedViewCourses(v *view) []CourseID {
	out := make([]CourseID, 0, len(v.courses))
	for c := range v.courses {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
