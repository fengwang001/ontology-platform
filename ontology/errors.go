package ontology

import "errors"

// 整批拒绝时返回的可区分原因。
var (
	ErrNegativePriority = errors.New("ontology: priority must be non-negative")
	ErrDuplicateID      = errors.New("ontology: duplicate event id")
	ErrNoProcessable    = errors.New("ontology: no processable event")
)

// InvariantError 表示内部不变量被破坏。
type InvariantError struct {
	Reason string
}

func (e *InvariantError) Error() string {
	return "ontology: invariant violated: " + e.Reason
}
