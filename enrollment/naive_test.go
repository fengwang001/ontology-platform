package enrollment

import (
	"fmt"
	"sort"
)

// naive is an independent, deliberately unsophisticated model of the
// same rules, used only by the differential test. It keeps selections
// as plain slices, counts seats by scanning every student on every
// check, and validates hypothetical states by copying slices.
type naive struct {
	cfg      Config
	credits  map[CourseID]int
	sections map[SectionID]*Section
	prereq   map[CourseID]map[CourseID]bool
	coreq    map[CourseID]map[CourseID]bool
	mutex    map[CourseID]map[CourseID]bool
	records  map[StudentID]map[CourseID]int
	enroll   map[StudentID][]naiveEnroll
}

type naiveEnroll struct {
	course  CourseID
	section SectionID
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:      cfg,
		credits:  make(map[CourseID]int),
		sections: make(map[SectionID]*Section),
		prereq:   make(map[CourseID]map[CourseID]bool),
		coreq:    make(map[CourseID]map[CourseID]bool),
		mutex:    make(map[CourseID]map[CourseID]bool),
		records:  make(map[StudentID]map[CourseID]int),
		enroll:   make(map[StudentID][]naiveEnroll),
	}
}

func (n *naive) addCourse(id CourseID, credits int) { n.credits[id] = credits }

func (n *naive) addSection(id SectionID, course CourseID, capacity int, slots ...Slot) {
	set := make(map[Slot]bool, len(slots))
	for _, s := range slots {
		set[s] = true
	}
	n.sections[id] = &Section{ID: id, Course: course, Capacity: capacity, Slots: set}
}

func (n *naive) addPrereq(c, p CourseID) { naivePut(n.prereq, c, p) }

func (n *naive) addCoreq(a, b CourseID) {
	naivePut(n.coreq, a, b)
	naivePut(n.coreq, b, a)
}

func (n *naive) addMutex(a, b CourseID) {
	naivePut(n.mutex, a, b)
	naivePut(n.mutex, b, a)
}

func naivePut(rel map[CourseID]map[CourseID]bool, from, to CourseID) {
	if rel[from] == nil {
		rel[from] = make(map[CourseID]bool)
	}
	rel[from][to] = true
}

func (n *naive) setRecord(s StudentID, c CourseID, grade int) {
	if n.records[s] == nil {
		n.records[s] = make(map[CourseID]int)
	}
	n.records[s][c] = grade
}

func (n *naive) setCapacity(id SectionID, capacity int) *Error {
	if capacity < 0 {
		return &Error{Code: CodeInvalidParam, Section: id, Detail: "naive: negative capacity"}
	}
	sec, ok := n.sections[id]
	if !ok {
		return &Error{Code: CodeNotFound, Section: id, Detail: "naive: section not found"}
	}
	sec.Capacity = capacity
	return nil
}

// count recomputes the seats taken in a section by scanning everyone.
func (n *naive) count(id SectionID) int {
	total := 0
	for _, list := range n.enroll {
		for _, en := range list {
			if en.section == id {
				total++
			}
		}
	}
	return total
}

func enrolledInList(list []naiveEnroll, c CourseID) bool {
	for _, en := range list {
		if en.course == c {
			return true
		}
	}
	return false
}

// check judges one candidate against a (possibly hypothetical)
// selection list, in the same fixed priority order as the engine.
func (n *naive) check(s StudentID, list []naiveEnroll, batch map[CourseID]bool, course CourseID, sec *Section) *Error {
	if enrolledInList(list, course) {
		return &Error{Code: CodeAlreadyEnrolled, Student: s, Course: course, Section: sec.ID,
			Detail: "naive: course already selected"}
	}
	for _, m := range sortedCourses(n.mutex[course]) {
		if enrolledInList(list, m) {
			return &Error{Code: CodeMutexConflict, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("naive: mutex with selected %s", m)}
		}
		if g, ok := n.records[s][m]; ok && g >= n.cfg.PassLine {
			return &Error{Code: CodeMutexConflict, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("naive: mutex with passed %s", m)}
		}
	}
	for _, p := range sortedCourses(n.prereq[course]) {
		g, ok := n.records[s][p]
		if !ok || g < n.cfg.PassLine {
			return &Error{Code: CodePrereqUnmet, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("naive: prereq %s unmet", p)}
		}
	}
	for _, co := range sortedCourses(n.coreq[course]) {
		if !enrolledInList(list, co) && !batch[co] {
			return &Error{Code: CodeCoreqUnmet, Student: s, Course: course, Section: sec.ID,
				Detail: fmt.Sprintf("naive: coreq %s not selected", co)}
		}
	}
	total := n.credits[course]
	for _, en := range list {
		total += n.credits[en.course]
	}
	if total > n.cfg.MaxCredits {
		return &Error{Code: CodeCreditExceeded, Student: s, Course: course, Section: sec.ID,
			Detail: fmt.Sprintf("naive: credits %d > %d", total, n.cfg.MaxCredits)}
	}
	for _, en := range list {
		for slot := range sec.Slots {
			if n.sections[en.section].Slots[slot] {
				return &Error{Code: CodeTimeConflict, Student: s, Course: course, Section: sec.ID,
					Detail: fmt.Sprintf("naive: slot %d clashes with %s", slot, en.section)}
			}
		}
	}
	if n.count(sec.ID) >= sec.Capacity {
		return &Error{Code: CodeCapacityFull, Student: s, Course: course, Section: sec.ID,
			Detail: fmt.Sprintf("naive: section %s full", sec.ID)}
	}
	return nil
}

func (n *naive) batchEnroll(s StudentID, picks []Pick) *Error {
	if len(picks) == 0 {
		return &Error{Code: CodeInvalidParam, Student: s, Detail: "naive: empty batch"}
	}
	seen := make(map[CourseID]bool, len(picks))
	for _, p := range picks {
		if seen[p.Course] {
			return &Error{Code: CodeInvalidParam, Student: s, Course: p.Course,
				Detail: "naive: duplicate course in batch"}
		}
		seen[p.Course] = true
	}
	// Validate against a scratch copy; commit only if every pick passes.
	list := append([]naiveEnroll(nil), n.enroll[s]...)
	for _, p := range picks {
		if _, ok := n.credits[p.Course]; !ok {
			return &Error{Code: CodeNotFound, Student: s, Course: p.Course, Section: p.Section,
				Detail: "naive: course not found"}
		}
		sec, ok := n.sections[p.Section]
		if !ok {
			return &Error{Code: CodeNotFound, Student: s, Course: p.Course, Section: p.Section,
				Detail: "naive: section not found"}
		}
		if sec.Course != p.Course {
			return &Error{Code: CodeInvalidParam, Student: s, Course: p.Course, Section: p.Section,
				Detail: "naive: section not of course"}
		}
		if err := n.check(s, list, seen, p.Course, sec); err != nil {
			return err
		}
		list = append(list, naiveEnroll{course: p.Course, section: p.Section})
	}
	n.enroll[s] = list
	return nil
}

func (n *naive) drop(s StudentID, c CourseID) *Error {
	if _, ok := n.credits[c]; !ok {
		return &Error{Code: CodeNotFound, Student: s, Course: c, Detail: "naive: course not found"}
	}
	if !enrolledInList(n.enroll[s], c) {
		return &Error{Code: CodeNotEnrolled, Student: s, Course: c, Detail: "naive: not selected"}
	}
	n.enroll[s] = removeCourse(n.enroll[s], c)
	// Cascade to a fixpoint: keep only courses whose coreqs all survive.
	for {
		var kept []naiveEnroll
		removed := false
		for _, en := range n.enroll[s] {
			supported := true
			for co := range n.coreq[en.course] {
				if !enrolledInList(n.enroll[s], co) {
					supported = false
					break
				}
			}
			if supported {
				kept = append(kept, en)
			} else {
				removed = true
			}
		}
		n.enroll[s] = kept
		if !removed {
			return nil
		}
	}
}

func removeCourse(list []naiveEnroll, c CourseID) []naiveEnroll {
	out := list[:0]
	for _, en := range list {
		if en.course != c {
			out = append(out, en)
		}
	}
	return out
}

func (n *naive) switchSec(s StudentID, oldSection, newSection SectionID) *Error {
	if oldSection == newSection {
		return &Error{Code: CodeInvalidParam, Student: s, Section: oldSection,
			Detail: "naive: switch to same section"}
	}
	oldSec, ok := n.sections[oldSection]
	if !ok {
		return &Error{Code: CodeNotFound, Student: s, Section: oldSection,
			Detail: "naive: old section not found"}
	}
	newSec, ok := n.sections[newSection]
	if !ok {
		return &Error{Code: CodeNotFound, Student: s, Section: newSection,
			Detail: "naive: new section not found"}
	}
	held := false
	for _, en := range n.enroll[s] {
		if en.course == oldSec.Course && en.section == oldSection {
			held = true
		}
	}
	if !held {
		return &Error{Code: CodeNotEnrolled, Student: s, Course: oldSec.Course, Section: oldSection,
			Detail: "naive: old section not selected"}
	}
	// Hypothetical: old dropped, new added; commit only if all passes.
	list := removeCourse(append([]naiveEnroll(nil), n.enroll[s]...), oldSec.Course)
	if err := n.check(s, list, nil, newSec.Course, newSec); err != nil {
		return err
	}
	list = append(list, naiveEnroll{course: newSec.Course, section: newSection})
	var unsupported []CourseID
	for _, en := range list {
		for co := range n.coreq[en.course] {
			if !enrolledInList(list, co) {
				unsupported = append(unsupported, en.course)
				break
			}
		}
	}
	if len(unsupported) > 0 {
		sort.Slice(unsupported, func(i, j int) bool { return unsupported[i] < unsupported[j] })
		return &Error{Code: CodeCoreqUnmet, Student: s, Course: unsupported[0],
			Detail: "naive: coreq partner stranded by switch"}
	}
	n.enroll[s] = list
	return nil
}
