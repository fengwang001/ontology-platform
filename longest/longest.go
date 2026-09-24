// Package longest finds the longest balanced substring in one stack pass.
package longest

import (
	"errors"

	"ontology/balance"
	"ontology/scan"
)

// ErrSelfCheck means the reported substring is not balanced.
var ErrSelfCheck = errors.New("longest: self-check failed")

// Result is the answer for one string.
type Result struct {
	Start  int
	Length int

	// accesses is the unexported character-access counter; never part of the
	// public input/output contract.
	accesses int
}

// Accesses returns the number of characters visited (test observability).
func (r Result) Accesses() int { return r.accesses }

// Solver runs the single-pass stack algorithm.
type Solver struct {
	Checker *balance.Checker
}

// New returns a Solver with a fresh scanner/checker using limit.
func New(limit int) *Solver {
	sc := scan.New(limit)
	return &Solver{Checker: balance.NewChecker(sc)}
}

// Solve returns the longest balanced substring of str.
func (s *Solver) Solve(str string) (Result, error) {
	return Result{}, errors.New("longest: not implemented")
}

// SelfCheck verifies that r describes a genuinely balanced substring of str
// with a non-negative, in-range span.
func (s *Solver) SelfCheck(str string, r Result) error {
	return errors.New("longest: not implemented")
}
