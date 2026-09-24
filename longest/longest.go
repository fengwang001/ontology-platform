// Package longest finds the longest balanced substring in one pass.
package longest

import (
	"fmt"
	"sync/atomic"

	"ontology/balance"
	"ontology/scan"
)

// Solver finds longest balanced substrings. Safe for concurrent use:
// the only shared state is the visit counter.
type Solver struct {
	chk    *balance.Checker
	visits atomic.Int64 // char accesses; unexported, never in the API
}

// New binds a Solver to a limit-checking Checker.
func New(chk *balance.Checker) *Solver { return &Solver{chk: chk} }

// Longest returns the start index and length of the longest balanced
// substring of input; ties resolve to the smallest start. Rejected
// inputs (invalid char, over limit) return zero values and the error,
// leaving no partial result.
//
// Stack of indices, pre-seeded with base -1; an unmatched ')' pushes
// its own index as the new base (see NOTES.md). Length = i - top.
func (s *Solver) Longest(input string) (start, length int, err error) {
	if err := s.chk.CheckLimit(input); err != nil {
		return 0, 0, err
	}
	stack := []int{-1}
	best, bestStart := 0, 0
	for i := 0; i < len(input); i++ {
		s.visits.Add(1)
		switch scan.Classify(input[i]) {
		case scan.Left:
			stack = append(stack, i)
		case scan.Right:
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				stack = append(stack, i)
			} else if l := i - stack[len(stack)-1]; l > best {
				best, bestStart = l, stack[len(stack)-1]+1
			}
		default:
			return 0, 0, fmt.Errorf("%w %q at index %d", balance.ErrInvalidChar, input[i], i)
		}
	}
	return bestStart, best, nil
}

// SelfCheck verifies that input[start:start+length] is balanced and
// that no longer balanced substring exists (naive enumeration).
func (s *Solver) SelfCheck(input string, start, length int) bool {
	if start < 0 || length < 0 || start+length > len(input) {
		return false
	}
	ok, err := s.chk.IsBalanced(input[start : start+length])
	if err != nil || !ok {
		return false
	}
	return length == naiveLen(input)
}

// naiveLen enumerates every substring in O(n^2) to find the longest
// balanced length; used only by SelfCheck.
func naiveLen(s string) int {
	best := 0
	for i := 0; i < len(s); i++ {
		depth := 0
		for j := i; j < len(s); j++ {
			if s[j] == '(' {
				depth++
			} else {
				depth--
			}
			if depth < 0 {
				break
			}
			if depth == 0 && j-i+1 > best {
				best = j - i + 1
			}
		}
	}
	return best
}
