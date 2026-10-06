package enrollment

import "fmt"

// Code classifies rejections. Declaration order is the fixed priority:
// when several violations apply, the one declared first is reported.
type Code int

const (
	// CodeInvalidParam: malformed request (empty batch, duplicated course
	// in a batch, section not belonging to the named course, switching to
	// the same section, negative capacity).
	CodeInvalidParam Code = iota
	// CodeNotFound: unknown course or section.
	CodeNotFound
	// CodeAlreadyEnrolled: the course is already selected this term.
	CodeAlreadyEnrolled
	// CodeMutexConflict: a mutually exclusive course is selected this
	// term or was passed in a past term.
	CodeMutexConflict
	// CodePrereqUnmet: a direct prerequisite is missing or below the
	// pass line.
	CodePrereqUnmet
	// CodeCoreqUnmet: a corequisite is neither selected nor in the batch.
	CodeCoreqUnmet
	// CodeCreditExceeded: the term credit ceiling would be exceeded.
	CodeCreditExceeded
	// CodeTimeConflict: a time slot overlaps an already selected section.
	CodeTimeConflict
	// CodeCapacityFull: the section has no free seat.
	CodeCapacityFull
	// CodeNotEnrolled: dropping or switching away a course/section the
	// student has not selected. Distinct from the priority list above;
	// it only applies to drop/switch requests.
	CodeNotEnrolled
)

func (c Code) String() string {
	switch c {
	case CodeInvalidParam:
		return "invalid_param"
	case CodeNotFound:
		return "not_found"
	case CodeAlreadyEnrolled:
		return "already_enrolled"
	case CodeMutexConflict:
		return "mutex_conflict"
	case CodePrereqUnmet:
		return "prereq_unmet"
	case CodeCoreqUnmet:
		return "coreq_unmet"
	case CodeCreditExceeded:
		return "credit_exceeded"
	case CodeTimeConflict:
		return "time_conflict"
	case CodeCapacityFull:
		return "capacity_full"
	case CodeNotEnrolled:
		return "not_enrolled"
	}
	return "unknown"
}

// Error is a rejected operation. A nil *Error means success.
type Error struct {
	Code    Code
	Student StudentID
	Course  CourseID  // offending course (first failing pick for batches)
	Section SectionID // offending section, when known
	Detail  string    // human-readable judgement basis
}

func (e *Error) Error() string {
	return fmt.Sprintf("enrollment: %s: student=%q course=%q section=%q: %s",
		e.Code, e.Student, e.Course, e.Section, e.Detail)
}
