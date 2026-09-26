// Package query encapsulates a single longest-common-substring query
// against a fixed reference string, yielding length, start index and the
// actual substring. It depends only on the dp package.
package query

import "ontology/dp"

// Result is the outcome of one query.
type Result struct {
	Length int    // length of the longest common substring
	Start  int    // 0-based start of that substring in the reference a
	Text   string // the substring itself, a[Start : Start+Length]
}

// Query runs queries against one fixed reference string.
type Query struct {
	a   string
	sol *dp.Solver
}

// New binds a reference string a.
func New(a string) *Query {
	return &Query{a: a, sol: dp.New(a)}
}

// Run answers one query for b. The rolling row is allocated inside the
// dp layer per call, so Run may be invoked concurrently.
func (q *Query) Run(b string) Result {
	l, st := q.sol.Solve(b)
	return Result{Length: l, Start: st, Text: q.a[st : st+l]}
}
