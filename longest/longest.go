// Package longest finds the longest balanced substring in one pass.
package longest

import (
	"sync/atomic"

	"ontology/balance"
	"ontology/scan"
)

// stats is unexported: charVisits proves the scan is single-pass.
var stats struct {
	charVisits atomic.Int64
}

// Longest returns the start and length of the longest balanced
// substring of s; ties resolve to the smallest start. Rejected
// input yields zero results plus an error, never partial output.
func Longest(s string) (int, int, error) {
	if err := balance.Validate(s); err != nil {
		return 0, 0, err
	}
	stack := []int{-1}
	best, start := 0, 0
	for i := 0; i < len(s); i++ {
		stats.charVisits.Add(1)
		if scan.Classify(s[i]) == scan.Left {
			stack = append(stack, i)
			continue
		}
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			stack = append(stack, i)
			continue
		}
		if n := i - stack[len(stack)-1]; n > best {
			best, start = n, i-n+1
		}
	}
	return start, best, nil
}

// SelfCheck verifies that s[start:start+length] is balanced and
// that length matches the naive exhaustive answer naiveLen.
func SelfCheck(s string, start, length, naiveLen int) bool {
	if start < 0 || length < 0 || start+length > len(s) || length != naiveLen {
		return false
	}
	ok, err := balance.IsBalanced(s[start : start+length])
	return err == nil && ok
}
