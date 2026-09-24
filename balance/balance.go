// Package balance decides whether a parentheses string is balanced.
package balance

import (
	"errors"

	"ontology/scan"
)

// Sentinel errors for the two failure kinds.
var (
	ErrUnexpectedRight = errors.New("balance: unmatched ')'")
	ErrUnclosedLeft    = errors.New("balance: unclosed '('")
)

// FailureKind distinguishes the two balance errors.
type FailureKind int

const (
	UnexpectedRight FailureKind = iota
	UnclosedLeft
)

// Failure locates the first position that breaks the balance condition.
type Failure struct {
	Index int
	Kind  FailureKind
}

// Checker checks strings with a shared scanner configuration.
type Checker struct {
	Scanner *scan.Scanner
}

// NewChecker returns a Checker using the given scanner.
func NewChecker(sc *scan.Scanner) *Checker { return &Checker{Scanner: sc} }

// Check returns (true, nil) when str is balanced, otherwise (false, Failure).
func (c *Checker) Check(str string) (bool, *Failure, error) {
	opens := make([]int, 0, len(str))
	var firstFailure *Failure
	err := c.Scanner.Events(str, func(ev scan.Event) bool {
		if firstFailure != nil {
			return false
		}
		switch ev.Kind {
		case scan.Left:
			opens = append(opens, ev.Index)
		case scan.Right:
			if len(opens) == 0 {
				firstFailure = &Failure{Index: ev.Index, Kind: UnexpectedRight}
				return false
			}
			opens = opens[:len(opens)-1]
		}
		return true
	})
	if err != nil {
		return false, nil, err
	}
	if firstFailure != nil {
		return false, firstFailure, nil
	}
	if len(opens) > 0 {
		return false, &Failure{Index: opens[0], Kind: UnclosedLeft}, nil
	}
	return true, nil, nil
}

// IsBalanced reports only the boolean result.
func (c *Checker) IsBalanced(str string) (bool, error) {
	ok, _, err := c.Check(str)
	return ok, err
}
