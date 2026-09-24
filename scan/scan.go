// Package scan classifies the characters of a parentheses string.
package scan

import "errors"

// Sentinel errors. ErrIllegal and ErrTooLong carry the offending index/length
// via IllegalIndex / TooLongLength from errors.As.
var (
	ErrBadLimit   = errors.New("scan: limit must be positive")
	ErrTooLong    = errors.New("scan: input exceeds configured limit")
	ErrIllegal    = errors.New("scan: illegal character")
)

// IllegalIndex reports the index of the first illegal character.
type IllegalIndex struct {
	Index int
	Err   error
}

func (e *IllegalIndex) Error() string { return e.Err.Error() }
func (e *IllegalIndex) Unwrap() error { return e.Err }

// TooLongLength reports the rejected length.
type TooLongLength struct {
	Length int
	Err    error
}

func (e *TooLongLength) Error() string { return e.Err.Error() }
func (e *TooLongLength) Unwrap() error { return e.Err }

// Kind classifies a character.
type Kind int

const (
	Left Kind = iota
	Right
	Illegal
)

// Event is one classified character.
type Event struct {
	Index int
	Kind  Kind
}

// Scanner classifies a string under a configurable length limit.
type Scanner struct {
	limit int
}

// New returns a Scanner. limit must be positive.
func New(limit int) *Scanner { return &Scanner{limit: limit} }

// Events streams events in a single pass via yield.
func (s *Scanner) Events(str string, yield func(Event) bool) error {
	if s.limit <= 0 {
		return ErrBadLimit
	}
	if len(str) > s.limit {
		return &TooLongLength{Length: len(str), Err: ErrTooLong}
	}
	for i := 0; i < len(str); i++ {
		var kind Kind
		switch str[i] {
		case '(':
			kind = Left
		case ')':
			kind = Right
		default:
			return &IllegalIndex{Index: i, Err: ErrIllegal}
		}
		if !yield(Event{Index: i, Kind: kind}) {
			return nil
		}
	}
	return nil
}
