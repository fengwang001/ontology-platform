// Package ontology implements a deterministic grading task allocation
// and double-marking arbitration engine.
package ontology

// EventKind enumerates the auditable events.
type EventKind string

const (
	EventAssign      EventKind = "assign"      // a reviewer was selected for a task
	EventWithdraw    EventKind = "withdraw"    // a task was withdrawn before submission
	EventSubmit      EventKind = "submit"      // a valid score was submitted
	EventArbitration EventKind = "arbitration" // a gap over the threshold opened an arbitration task
	EventFinal       EventKind = "final"       // a final score was produced
)

// Group is a marking group. Two initial reviewers of one sheet must come
// from different groups; the arbitrator must come from a third group.
type Group struct {
	ID string
}

// Question is the question a sheet belongs to. MaxScore must be a positive
// multiple of Step; Threshold is the inclusive agreement threshold.
type Question struct {
	ID        string
	MaxScore  int
	Step      int
	Threshold int
}

// Reviewer belongs to a group, has a daily quota, and may be deactivated.
type Reviewer struct {
	ID      string
	GroupID string
	Quota   int
	Active  bool
}

// Sheet is an answer sheet.
type Sheet struct {
	ID       string
	Question string
	Student  string
}

// TaskView is the externally visible state of one marking task.
type TaskView struct {
	ID         string
	SheetID    string
	ReviewerID string
	Role       string // "initial" or "arbitrator"
	Score      *int   // submitted score; nil while unsubmitted
	Withdrawn  bool
}

// SheetView is the externally visible state of one sheet.
type SheetView struct {
	ID          string
	Question    string
	Student     string
	FinalScore  *int
	PendingTask bool // one of the sheet's tasks waits for a feasible reviewer
	Tasks       []TaskView
}

// Event is one immutable audit entry.
type Event struct {
	Seq      int
	Kind     EventKind
	Sheet    string
	TaskID   string
	Reviewer string // selected/submitting reviewer (empty when not applicable)
	Role     string
	Score    *int // submit/final score when applicable
	Basis    string
}

// View is the full engine snapshot, used by callers and by the naive
// cross-check to compare two independent implementations.
type View struct {
	Reviewers map[string]Reviewer
	Questions map[string]Question
	Groups    map[string]Group
	Sheets    []SheetView
	Events    []Event
}
