package enrollment

import (
	"fmt"
	"sort"
)

// view is a hypothetical snapshot of one student's selections used to
// validate batches and switches without touching real state.
type view struct {
	courses map[CourseID]SectionID
	slots   map[Slot]SectionID
	credits int
	// batch holds every course of the batch being validated (nil for
	// switches). A corequisite is satisfied when it is already selected
	// or appears anywhere in the same batch; credits and time, by
	// contrast, accumulate only over earlier picks via add().
	batch map[CourseID]bool
}

func (e *Engine) newView(s StudentID) *view {
	v := &view{
		courses: make(map[CourseID]SectionID, len(e.enrolled[s])),
		slots:   make(map[Slot]SectionID, len(e.occupied[s])),
		credits: e.credits[s],
	}
	for c, sec := range e.enrolled[s] {
		v.courses[c] = sec
	}
	for slot, sec := range e.occupied[s] {
		v.slots[slot] = sec
	}
	return v
}

func (v *view) add(e *Engine, sec *Section) {
	v.courses[sec.Course] = sec.ID
	for slot := range sec.Slots {
		v.slots[slot] = sec.ID
	}
	v.credits += e.courses[sec.Course].Credits
}

func (v *view) remove(e *Engine, sec *Section) {
	delete(v.courses, sec.Course)
	for slot := range sec.Slots {
		delete(v.slots, slot)
	}
	v.credits -= e.courses[sec.Course].Credits
}

// resolve validates the existence of a pick's course and section and
// their correspondence.
func (e *Engine) resolve(s StudentID, p Pick) (*Section, *Error) {
	if _, ok := e.courses[p.Course]; !ok {
		return nil, &Error{Code: CodeNotFound, Student: s, Course: p.Course, Section: p.Section,
			Detail: "course not found"}
	}
	sec, ok := e.sections[p.Section]
	if !ok {
		return nil, &Error{Code: CodeNotFound, Student: s, Course: p.Course, Section: p.Section,
			Detail: "section not found"}
	}
	if sec.Course != p.Course {
		return nil, &Error{Code: CodeInvalidParam, Student: s, Course: p.Course, Section: p.Section,
			Detail: "section does not belong to course"}
	}
	return sec, nil
}

// checkEnroll judges one candidate against a view, in fixed priority
// order; the first violated constraint is reported.
func (e *Engine) checkEnroll(s StudentID, v *view, course CourseID, sec *Section) *Error {
	if _, ok := v.courses[course]; ok {
		return &Error{Code: CodeAlreadyEnrolled, Student: s, Course: course, Section: sec.ID,
			Detail: "course already selected this term"}
	}
	for _, m := range sortedCourses(e.mutex[course]) {
		if _, ok := v.courses[m]; ok {
			return &Error{Code: CodeMutexConflict, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("mutex with selected course %s", m)}
		}
		if g, ok := e.records[s][m]; ok && g >= e.cfg.PassLine {
			return &Error{Code: CodeMutexConflict, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("mutex with passed course %s (grade %d)", m, g)}
		}
	}
	for _, p := range sortedCourses(e.prereq[course]) {
		g, ok := e.records[s][p]
		if !ok {
			return &Error{Code: CodePrereqUnmet, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("prerequisite %s never taken", p)}
		}
		if g < e.cfg.PassLine {
			return &Error{Code: CodePrereqUnmet, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("prerequisite %s grade %d below pass line %d", p, g, e.cfg.PassLine)}
		}
	}
	for _, co := range sortedCourses(e.coreq[course]) {
		if _, ok := v.courses[co]; !ok && !v.batch[co] {
			return &Error{Code: CodeCoreqUnmet, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("corequisite %s not selected", co)}
		}
	}
	if v.credits+e.courses[course].Credits > e.cfg.MaxCredits {
		return &Error{Code: CodeCreditExceeded, Student: s, Course: course, Section: sec.ID,
			Detail: fmt.Sprintf("credits %d + %d exceed ceiling %d",
				v.credits, e.courses[course].Credits, e.cfg.MaxCredits)}
	}
	for _, slot := range sortedSlots(sec.Slots) {
		e.stats.SlotProbes++
		if holder, ok := v.slots[slot]; ok {
			return &Error{Code: CodeTimeConflict, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("slot %d already held by section %s", slot, holder)}
		}
	}
	e.stats.CapacityProbes++
	if e.ledger[sec.ID] >= sec.Capacity {
		return &Error{Code: CodeCapacityFull, Student: s, Course: course, Section: sec.ID,
			Detail: fmt.Sprintf("section %s full: %d/%d", sec.ID, e.ledger[sec.ID], sec.Capacity)}
	}
	return nil
}

func sortedCourses(m map[CourseID]bool) []CourseID {
	out := make([]CourseID, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedSlots(m map[Slot]bool) []Slot {
	out := make([]Slot, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
