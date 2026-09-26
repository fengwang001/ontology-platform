package cell

import (
	"errors"
	"strconv"
)

var (
	ErrBareQuote       = errors.New("bare quote in unquoted field")
	ErrAfterQuote      = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote   = errors.New("unclosed quoted field at end of input")
	ErrLoneCR          = errors.New("lone carriage return")
	ErrFieldCount      = errors.New("record field count mismatch")
	ErrFieldTooLarge   = errors.New("field exceeds max bytes")
	ErrTooManyFields   = errors.New("record exceeds max field count")
	ErrTooManyRecords  = errors.New("input exceeds max record count")
	ErrClosed          = errors.New("parser already in terminal state")
)

// Cell is one field. Quoted distinguishes "" from an unquoted empty field.
// Start is inclusive, End is exclusive byte offset in the original input.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int64
	End    int64
}

// PosError wraps a sentinel with 1-based record/field and 0-based byte offset.
type PosError struct {
	Kind   error
	Offset int64
	Record int64
	Field  int64
}

func (e *PosError) Error() string {
	if e == nil {
		return ""
	}
	return e.Kind.Error() + " at byte " + strconv.FormatInt(e.Offset, 10) +
		" (record " + strconv.FormatInt(e.Record, 10) +
		", field " + strconv.FormatInt(e.Field, 10) + ")"
}

func (e *PosError) Unwrap() error { return e.Kind }

// Is lets errors.Is match the sentinel Kind.
func (e *PosError) Is(target error) bool { return e != nil && e.Kind == target }
