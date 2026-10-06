package enrollment

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid_argument"
	ErrCourseNotFound  ErrorCode = "course_not_found"
	ErrSectionNotFound ErrorCode = "section_not_found"
	ErrAlreadyEnrolled ErrorCode = "already_enrolled"
	ErrMutualExclusion ErrorCode = "mutual_exclusion"
	ErrPrerequisite    ErrorCode = "prerequisite_unsatisfied"
	ErrCorequisite     ErrorCode = "corequisite_unsatisfied"
	ErrCreditLimit     ErrorCode = "credit_limit_exceeded"
	ErrTimeConflict    ErrorCode = "time_conflict"
	ErrCapacityFull    ErrorCode = "capacity_full"
	ErrNotEnrolled     ErrorCode = "not_enrolled"
)

type RuleError struct {
	Code    ErrorCode
	Message string
	Index   int
}

func (e *RuleError) Error() string { return e.Message }

type Course struct {
	ID      string
	Credits int
}

type Section struct {
	ID       string
	CourseID string
	Capacity int
	Times    []int
}

type Request struct {
	CourseID  string
	SectionID string
}
