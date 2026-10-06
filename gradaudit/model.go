// Package gradaudit implements a deterministic credit-substitution and
// graduation-audit engine.
//
// Responsibilities:
//   - model.go: curriculum plans, requirement tree, students, records, errors;
//   - engine.go: stateful, concurrency-safe operations and validation;
//   - counting.go: which enrollments count (pass line, repeats, transfer cap,
//     substitutions) and which labels each counted record may claim;
//   - assign.go: exhaustive assignment enumeration over the requirement tree,
//     deterministic verdict, attribution and gap computation;
//   - audit.go: audit result assembly including additional graduation clauses;
//   - naive_test.go: an independently written brute-force oracle.
package gradaudit

import "fmt"

// ErrorKind is the fixed-priority classification of rejected operations.
// Smaller values have higher priority.
type ErrorKind int

const (
	ErrInvalid ErrorKind = iota + 1
	ErrNotFound
	ErrAlreadyRevoked
	ErrSubNotApplicable
	ErrTransferOverflow
	ErrVersionTooOld
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalid:
		return "invalid argument"
	case ErrNotFound:
		return "student/course/plan version not found"
	case ErrAlreadyRevoked:
		return "record already revoked"
	case ErrSubNotApplicable:
		return "substitution not applicable to plan version"
	case ErrTransferOverflow:
		return "transfer credit exceeds cap"
	case ErrVersionTooOld:
		return "target plan version is older than current"
	default:
		return "unknown error"
	}
}

// OpError carries a classified error kind. Rejected operations never mutate
// engine state.
type OpError struct {
	Kind ErrorKind
	Msg  string
}

func (e *OpError) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

func opError(kind ErrorKind, format string, args ...any) *OpError {
	return &OpError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Course is a catalog entry with canonical credits.
type Course struct {
	Code   string
	Credit float64
}

// Requirement is a node of the requirement tree. Leaf requirements list the
// courses that may count toward them; internal nodes require at least
// MinChildren satisfied children (len(Children) means "all").
type Requirement struct {
	Code        string
	Leaf        bool
	Courses     []string // leaf only
	MinCredit   float64  // leaf only
	MinCourses  int      // leaf only; 0 is a legal value
	Children    []*Requirement
	MinChildren int  // internal only; <=0 means all children
	Required    bool // leaf only: courses counted here are required courses
}

// Satisfied evaluates the node against satisfiedLeaves.
func (r *Requirement) Satisfied(satisfied map[*Requirement]bool) bool {
	if r.Leaf {
		return satisfied[r]
	}
	n := 0
	for _, c := range r.Children {
		if c.Satisfied(satisfied) {
			n++
		}
	}
	need := r.MinChildren
	if need <= 0 || need > len(r.Children) {
		need = len(r.Children)
	}
	return n >= need
}

// PlanVersion is one immutable versioned curriculum plan.
type PlanVersion struct {
	ID          string
	PassScore   float64
	Root        *Requirement
	TotalCredit float64 // minimum counted total credits
	MinGPA      float64 // minimum credit-weighted average of counted records
	TransferCap float64 // maximum total transferred credits
	// SharedPairs declares unordered pairs of leaf requirement codes for which
	// the same course may count into both requirements.
	SharedPairs [][2]string
}

// Record is one enrollment or transferred enrollment.
type Record struct {
	ID       string
	Course   string
	Semester int
	Score    float64
	Credit   float64
	Transfer bool
	Revoked  bool
	// transferOrder is registration order among the student's transfer records;
	// it is explicitly part of the overflow rule.
	transferOrder int
	regOrder      int
}

// Substitution is an approved "taken course may count as target course"
// declaration, scoped to one plan version and effective from a semester.
type Substitution struct {
	From      string
	To        string
	PlanID    string
	Effective int // inclusive: semester >= Effective may use it
}

// Student binds a student to one plan version at enrollment.
type Student struct {
	ID     string
	PlanID string
}

// ReqGap describes the shortfall of an unsatisfied leaf or internal node.
type ReqGap struct {
	Code string
	Leaf bool
	// leaf gaps
	CreditGap float64
	CourseGap int
	// internal gap: number of additional satisfied children required
	ChildrenGap int
}

// Attribution identifies the smallest-coded requirement that cannot be
// satisfied under any assignment, together with its most-favorable gap.
type Attribution struct {
	Code string
	Gap  ReqGap
}

// AdditionalFailure enumerates one unmet additional graduation clause.
type AdditionalFailure struct {
	Kind       string
	Missing    float64
	CourseCode string
}

// AuditResult is the deterministic audit verdict.
type AuditResult struct {
	StudentID     string
	PlanID        string
	Pass          bool
	TreeSatisfied bool
	Attribution   *Attribution
	Additional    []AdditionalFailure
	CountedCredit float64
	WeightedGPA   float64
}
