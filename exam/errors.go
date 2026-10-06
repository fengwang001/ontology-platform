package exam

import "fmt"

// Category classifies rejected operations. When several rules are violated
// at once, the category with the smallest numeric value wins:
//
//	InvalidParam > NotFound > InvalidState > NotSelectable >
//	MutexConflict > TotalScore > KnowledgeCoverage > Difficulty
type Category int

const (
	ErrInvalidParam Category = iota + 1
	ErrNotFound
	ErrInvalidState
	ErrNotSelectable
	ErrMutexConflict
	ErrTotalScore
	ErrKnowledgeCoverage
	ErrDifficulty
)

func (c Category) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid-param"
	case ErrNotFound:
		return "not-found"
	case ErrInvalidState:
		return "invalid-state"
	case ErrNotSelectable:
		return "not-selectable"
	case ErrMutexConflict:
		return "mutex-conflict"
	case ErrTotalScore:
		return "total-score"
	case ErrKnowledgeCoverage:
		return "knowledge-coverage"
	case ErrDifficulty:
		return "difficulty-distribution"
	}
	return "unknown"
}

// Error is the only error type returned by Engine. Questions lists the
// question ids involved in the failure; Paper is the affected paper id.
type Error struct {
	Cat       Category
	Msg       string
	Paper     string
	Questions []string
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Cat, e.Msg)
}

func newError(cat Category, paper string, qs []string, format string, args ...any) *Error {
	return &Error{Cat: cat, Msg: fmt.Sprintf(format, args...), Paper: paper, Questions: qs}
}
