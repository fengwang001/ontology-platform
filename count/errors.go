package count

import "errors"

var (
	ErrInvalid  = errors.New("count: invalid argument")
	ErrNotFound = errors.New("count: not found")
	ErrState    = errors.New("count: illegal state")
	ErrConflict = errors.New("count: conflict")
	ErrRotate   = errors.New("count: must rotate counter")
	ErrStock    = errors.New("count: insufficient stock")
)

// Phase 为库位盘点阶段。
type Phase int

const (
	First Phase = iota
	Second
	Third
	Pending
	Done
)

func (p Phase) String() string {
	switch p {
	case First:
		return "First"
	case Second:
		return "Second"
	case Third:
		return "Third"
	case Pending:
		return "Pending"
	default:
		return "Done"
	}
}
