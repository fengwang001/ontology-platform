// Package query implements the three substring queries on a sam.Automaton,
// plus naive reference implementations used by tests and SelfCheck.
package query

import (
	"errors"
	"sort"

	"ontology/sam"
)

// ErrEmptyQuery is returned when a query string is empty.
var ErrEmptyQuery = errors.New("query: empty query string")

// Queries runs read-only queries on one automaton; safe for concurrent use.
type Queries struct{ a *sam.Automaton }

// New wraps automaton a.
func New(a *sam.Automaton) *Queries { return &Queries{a: a} }

// Distinct returns the number of distinct substrings:
// the sum of len[v]-len[link[v]] over all non-root states.
func (q *Queries) Distinct() int {
	total := 0
	q.a.ForEachState(func(v int) {
		if v != q.a.Root() {
			total += q.a.Len(v) - q.a.Len(q.a.Link(v))
		}
	})
	return total
}

// Occurrences returns how many times sub occurs in the original string.
// A substring that does not occur yields 0 (not an error).
func (q *Queries) Occurrences(sub string) (int, error) {
	if sub == "" {
		return 0, ErrEmptyQuery
	}
	v := q.a.Root()
	for i := 0; i < len(sub); i++ {
		u, ok := q.a.Next(v, sub[i])
		if !ok {
			return 0, nil
		}
		v = u
	}
	// Size of the endpos set of v: terminal counts propagated up the link tree.
	var ids []int
	q.a.ForEachState(func(x int) { ids = append(ids, x) })
	sort.Slice(ids, func(i, j int) bool { return q.a.Len(ids[i]) > q.a.Len(ids[j]) })
	occ := make([]int, len(ids))
	for _, x := range ids {
		if q.a.Terminal(x) {
			occ[x] = 1
		}
	}
	for _, x := range ids { // descending len: children before their link parents
		if l := q.a.Link(x); l >= 0 {
			occ[l] += occ[x]
		}
	}
	return occ[v], nil
}

// LongestCommonSubstring feeds t through the automaton, falling back along
// suffix links on missing transitions, and returns a longest common substring.
func (q *Queries) LongestCommonSubstring(t string) (string, error) {
	if t == "" {
		return "", ErrEmptyQuery
	}
	v, l, best, bestEnd := q.a.Root(), 0, 0, 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		for v != q.a.Root() {
			if _, ok := q.a.Next(v, c); ok {
				break
			}
			v = q.a.Link(v)
			l = q.a.Len(v)
		}
		if u, ok := q.a.Next(v, c); ok {
			v, l = u, l+1
		} else {
			v, l = q.a.Root(), 0
		}
		if l > best {
			best, bestEnd = l, i+1
		}
	}
	return t[bestEnd-best : bestEnd], nil
}

// NaiveDistinct counts distinct substrings by enumeration.
func NaiveDistinct(s string) int {
	set := map[string]struct{}{}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j <= len(s); j++ {
			set[s[i:j]] = struct{}{}
		}
	}
	return len(set)
}

// NaiveOccurrences counts occurrences of sub by scanning every start position.
func NaiveOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

// NaiveLCS returns a longest common substring of s and t (classic DP).
func NaiveLCS(s, t string) string {
	best := ""
	dp := make([]int, len(t)+1)
	for i := 1; i <= len(s); i++ {
		prev := 0
		for j := 1; j <= len(t); j++ {
			tmp := dp[j]
			if s[i-1] == t[j-1] {
				dp[j] = prev + 1
			} else {
				dp[j] = 0
			}
			if dp[j] > len(best) {
				best = t[j-dp[j] : j]
			}
			prev = tmp
		}
	}
	return best
}
