// Package enrollment implements a course-enrollment constraint engine:
// capacity accounting, prerequisite/corequisite/mutex relations, credit
// ceilings and time-conflict checks, with atomic batch enrollment,
// cascading drops and atomic section switches.
package enrollment

// StudentID identifies a student.
type StudentID string

// CourseID identifies a course. A course may offer several sections.
type CourseID string

// SectionID identifies a teaching section of a course.
type SectionID string

// Slot is a weekly time-slot index; two sections conflict when their
// slot sets intersect.
type Slot int

// Config carries the term-wide policy knobs.
type Config struct {
	// PassLine is the minimum passing grade; a grade equal to it counts
	// as passed.
	PassLine int
	// MaxCredits is the per-student per-term credit ceiling; a total
	// equal to it is allowed.
	MaxCredits int
}

// Course is a catalog entry.
type Course struct {
	ID      CourseID
	Credits int
}

// Section is a teaching section of a course with a capacity and a set of
// weekly time slots.
type Section struct {
	ID       SectionID
	Course   CourseID
	Capacity int
	Slots    map[Slot]bool
}

// Pick is one item of a batch enrollment request: a course and the
// desired section of that course.
type Pick struct {
	Course  CourseID
	Section SectionID
}
