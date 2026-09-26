// Package api is the public facade: a Calculator with a fixed distance cap.
package api

import (
	"errors"
	"fmt"

	"ontology/dist"
	"ontology/ops"
)

// maxLen bounds len(a)+len(b); larger inputs are rejected with ErrTooLong.
const maxLen = 1 << 20

var (
	// ErrNegativeCap rejects a negative k in New.
	ErrNegativeCap = errors.New("api: negative distance cap")
	// ErrTooLong rejects inputs whose combined length exceeds maxLen.
	ErrTooLong = errors.New("api: input too long")
	// ErrExceedsCap reports a true distance greater than the fixed cap.
	ErrExceedsCap = dist.ErrExceedsCap
)

// Calculator computes capped edit distances. Its only field is written once
// by New and never mutated, so rejected calls cannot leave traces.
type Calculator struct {
	k int
}

// New fixes the distance cap; a negative k is rejected with ErrNegativeCap.
func New(k int) (*Calculator, error) {
	if k < 0 {
		return nil, ErrNegativeCap
	}
	return &Calculator{k: k}, nil
}

// Distance returns the edit distance if it is at most the cap,
// ErrExceedsCap if it exceeds the cap, ErrTooLong for oversized inputs.
func (c *Calculator) Distance(a, b string) (int, error) {
	if len(a)+len(b) > maxLen {
		return 0, ErrTooLong
	}
	return dist.Distance(a, b, c.k)
}

// EditScript returns a minimal edit script turning a into b under the cap.
func (c *Calculator) EditScript(a, b string) ([]ops.Op, error) {
	if len(a)+len(b) > maxLen {
		return nil, ErrTooLong
	}
	return ops.EditScript(a, b, c.k)
}

// naive is the reference full-table DP, used only by SelfCheck.
func naive(a, b string) int {
	n, m := len(a), len(b)
	prev := make([]int, m+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		curr := make([]int, m+1)
		curr[0] = i
		for j := 1; j <= m; j++ {
			best := prev[j] + 1
			if v := curr[j-1] + 1; v < best {
				best = v
			}
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			if v := prev[j-1] + cost; v < best {
				best = v
			}
			curr[j] = best
		}
		prev = curr
	}
	return prev[m]
}

// SelfCheck verifies the four invariants on built-in inputs and returns a
// descriptive error on the first violation, nil when all hold.
func (c *Calculator) SelfCheck() error {
	pairs := [][2]string{
		{"abc", "yabd"}, {"", ""}, {"", "xyz"}, {"kitten", "sitting"},
		{"aaaa", "aaaa"}, {"flaw", "lawn"}, {"gumbo", "gambol"},
	}
	for _, p := range pairs { // invariant 1+3: banded == naive, cap iff
		want := naive(p[0], p[1])
		for k := 0; k <= want+1; k++ {
			calc, err := New(k)
			if err != nil {
				return err
			}
			d, err := calc.Distance(p[0], p[1])
			if k < want && !errors.Is(err, ErrExceedsCap) {
				return fmt.Errorf("selfcheck: (%q,%q) k=%d: want ErrExceedsCap, got %v", p[0], p[1], k, err)
			}
			if k >= want && (err != nil || d != want) {
				return fmt.Errorf("selfcheck: (%q,%q) k=%d: want %d, got %d, %v", p[0], p[1], k, want, d, err)
			}
		}
	}
	for _, p := range pairs { // invariant 2: script applies, length == distance
		calc, _ := New(naive(p[0], p[1]))
		script, err := calc.EditScript(p[0], p[1])
		if err != nil {
			return err
		}
		got, err := ops.Apply(p[0], script)
		d, _ := calc.Distance(p[0], p[1])
		if err != nil || got != p[1] || len(script) != d {
			return fmt.Errorf("selfcheck: bad script for (%q,%q)", p[0], p[1])
		}
	}
	// invariant 4: rejections leave no trace; distance-0 pair works for any k
	before, err := c.Distance("selfcheck", "selfcheck")
	if err != nil {
		return err
	}
	if _, err := c.Distance(string(make([]byte, maxLen)), "x"); !errors.Is(err, ErrTooLong) {
		return errors.New("selfcheck: oversized input not rejected")
	}
	if c.k < maxLen { // otherwise the too-long check would fire first
		if _, err := c.Distance("", string(make([]byte, c.k+1))); !errors.Is(err, ErrExceedsCap) {
			return errors.New("selfcheck: over-cap distance not rejected")
		}
	}
	after, err := c.Distance("selfcheck", "selfcheck")
	if err != nil || before != after {
		return errors.New("selfcheck: state changed after rejected calls")
	}
	return nil
}
