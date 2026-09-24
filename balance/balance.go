// Package balance decides whether a parenthesis string is balanced.
package balance

import (
	"errors"
	"fmt"

	"ontology/scan"
)

// Sentinel errors; distinguishable with errors.Is.
var (
	ErrInvalidLimit = errors.New("balance: limit must be positive")
	ErrTooLong      = errors.New("balance: input length exceeds limit")
	ErrInvalidChar  = errors.New("balance: invalid character")
	ErrUnbalanced   = errors.New("balance: unbalanced input")
)

// FailKind classifies how balancedness is first broken.
type FailKind int

const (
	ExcessRight  FailKind = iota // ')' with nothing to match
	UnclosedLeft                 // '(' never closed
)

func (k FailKind) String() string {
	if k == ExcessRight {
		return "unmatched ')'"
	}
	return "unclosed '('"
}

// Failure is the first position that breaks balancedness.
type Failure struct {
	Pos  int
	Kind FailKind
}

func (f *Failure) Error() string { return fmt.Sprintf("balance: %s at index %d", f.Kind, f.Pos) }

func (f *Failure) Unwrap() error { return ErrUnbalanced }

// Checker validates inputs up to a configurable length limit.
// It is safe for concurrent use.
type Checker struct{ maxLen int }

// New rejects a non-positive limit.
func New(maxLen int) (*Checker, error) {
	if maxLen <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLimit, maxLen)
	}
	return &Checker{maxLen: maxLen}, nil
}

// CheckLimit validates only the length of s.
func (c *Checker) CheckLimit(s string) error {
	if len(s) > c.maxLen {
		return fmt.Errorf("%w: %d > %d", ErrTooLong, len(s), c.maxLen)
	}
	return nil
}

// IsBalanced reports whether s is balanced in a single pass. The stack
// holds indices of unmatched '('; its depth equals the running counter,
// so the first failure position is where the counter would go negative,
// or the first unclosed '(' when the counter ends positive.
func (c *Checker) IsBalanced(s string) (bool, error) {
	if err := c.CheckLimit(s); err != nil {
		return false, err
	}
	var stack []int
	for i := 0; i < len(s); i++ {
		switch scan.Classify(s[i]) {
		case scan.Left:
			stack = append(stack, i)
		case scan.Right:
			if len(stack) == 0 {
				return false, &Failure{Pos: i, Kind: ExcessRight}
			}
			stack = stack[:len(stack)-1]
		default:
			return false, fmt.Errorf("%w %q at index %d", ErrInvalidChar, s[i], i)
		}
	}
	if len(stack) > 0 {
		return false, &Failure{Pos: stack[0], Kind: UnclosedLeft}
	}
	return true, nil
}
