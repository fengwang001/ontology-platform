// Package api is the public facade of the suffix-automaton service.
package api

import (
	"fmt"
	"strings"

	"ontology/query"
	"ontology/sam"
)

// Sentinel errors, re-exported so callers can use errors.Is.
var (
	ErrEmptyInput = sam.ErrEmpty
	ErrTooLong    = sam.ErrTooLong
	ErrEmptyQuery = query.ErrEmptyQuery
)

// API is a built suffix automaton; all methods are safe for concurrent use.
type API struct {
	a *sam.Automaton
	q *query.Queries
}

// New builds the automaton for s. Rejected input leaves no state behind.
func New(s string) (*API, error) {
	a, err := sam.New(s)
	if err != nil {
		return nil, err
	}
	return &API{a: a, q: query.New(a)}, nil
}

// DistinctSubstrings returns the number of distinct substrings.
func (x *API) DistinctSubstrings() int { return x.q.Distinct() }

// Occurrences counts occurrences of sub; a missing substring returns 0, nil.
func (x *API) Occurrences(sub string) (int, error) { return x.q.Occurrences(sub) }

// LongestCommonSubstring returns a longest common substring of s and t.
func (x *API) LongestCommonSubstring(t string) (string, error) {
	return x.q.LongestCommonSubstring(t)
}

// SelfCheck verifies the four invariants on a set of built-in strings.
// It only reads existing state and builds fresh automata, so it is safe
// for concurrent use and never mutates the receiver.
func (x *API) SelfCheck() error {
	builtins := []string{"abcbc", "aaaa", "ababa", "mississippi"}
	for _, s := range builtins {
		a, err := sam.New(s)
		if err != nil {
			return err
		}
		q := query.New(a)
		// Invariant 1: agree with the naive reference.
		if q.Distinct() != query.NaiveDistinct(s) {
			return fmt.Errorf("selfcheck: distinct mismatch for %q", s)
		}
		for _, t := range builtins {
			got, err := q.LongestCommonSubstring(t)
			if err != nil || len(got) != len(query.NaiveLCS(s, t)) ||
				!strings.Contains(s, got) || !strings.Contains(t, got) {
				return fmt.Errorf("selfcheck: lcs mismatch for %q vs %q", s, t)
			}
		}
		// Invariant 2: link tree — len strictly decreases and reaches the root.
		shapeOK := true
		a.ForEachState(func(v int) {
			if v == a.Root() {
				return
			}
			if a.Len(v) <= a.Len(a.Link(v)) {
				shapeOK = false
			}
			seen := map[int]bool{}
			for u := v; u != a.Root(); u = a.Link(u) {
				if u < 0 || seen[u] {
					shapeOK = false
					return
				}
				seen[u] = true
			}
		})
		if !shapeOK {
			return fmt.Errorf("selfcheck: link tree broken for %q", s)
		}
		// Invariant 3: occurrence counts equal naive counts, missing -> 0.
		for i := 0; i < len(s); i++ {
			for j := i + 1; j <= len(s); j++ {
				sub := s[i:j]
				if got, _ := q.Occurrences(sub); got != query.NaiveOccurrences(s, sub) {
					return fmt.Errorf("selfcheck: occurrences mismatch for %q in %q", sub, s)
				}
			}
		}
		if got, _ := q.Occurrences(s + "!"); got != 0 {
			return fmt.Errorf("selfcheck: missing substring not 0 for %q", s)
		}
		// Invariant 4: rejected operations leave no trace.
		d := q.Distinct()
		_, e1 := sam.New("")
		_, e2 := sam.New(strings.Repeat("x", sam.MaxLen+1))
		_, e3 := q.Occurrences("")
		_, e4 := q.LongestCommonSubstring("")
		if e1 == nil || e2 == nil || e3 == nil || e4 == nil {
			return fmt.Errorf("selfcheck: invalid input not rejected for %q", s)
		}
		if q.Distinct() != d {
			return fmt.Errorf("selfcheck: state changed after rejection for %q", s)
		}
	}
	return nil
}
