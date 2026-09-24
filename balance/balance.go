// Package balance decides whether a parenthesis string is balanced.
package balance

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/scan"
)

// Sentinel errors; distinguishable via errors.Is.
var (
	ErrBadLimit        = errors.New("balance: max length must be positive")
	ErrTooLong         = errors.New("balance: input exceeds max length")
	ErrInvalidChar     = errors.New("balance: invalid character")
	ErrUnexpectedRight = errors.New("balance: unmatched right parenthesis")
	ErrUnclosedLeft    = errors.New("balance: unclosed left parenthesis")
)

// PosError pinpoints the first offending index.
type PosError struct {
	Pos int
	Err error
}

func (e *PosError) Error() string { return fmt.Sprintf("index %d: %v", e.Pos, e.Err) }
func (e *PosError) Unwrap() error { return e.Err }

var maxLen atomic.Int64

func init() { maxLen.Store(1 << 20) }

// SetMaxLength configures the input length cap; non-positive is rejected.
func SetMaxLength(n int) error {
	if n <= 0 {
		return ErrBadLimit
	}
	maxLen.Store(int64(n))
	return nil
}

// Validate rejects oversize input and input containing invalid bytes.
func Validate(s string) error {
	if len(s) > int(maxLen.Load()) {
		return ErrTooLong
	}
	if i := scan.FirstInvalid(s); i >= 0 {
		return &PosError{Pos: i, Err: ErrInvalidChar}
	}
	return nil
}

// IsBalanced reports whether s is balanced. When it is not, the error
// carries the first index that breaks the counting invariant.
func IsBalanced(s string) (bool, error) {
	if err := Validate(s); err != nil {
		return false, err
	}
	var stack []int
	for i := 0; i < len(s); i++ {
		switch scan.Classify(s[i]) {
		case scan.Left:
			stack = append(stack, i)
		case scan.Right:
			if len(stack) == 0 {
				return false, &PosError{Pos: i, Err: ErrUnexpectedRight}
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return false, &PosError{Pos: stack[0], Err: ErrUnclosedLeft}
	}
	return true, nil
}
