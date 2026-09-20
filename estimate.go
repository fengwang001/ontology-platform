package ontology

import (
	"math"
	"math/rand"
)

// sampleSeed is fixed so estimates are deterministic for a given data set.
const sampleSeed = 42

// confidenceDelta is the failure probability of the sampling error bound.
const confidenceDelta = 0.001

// EstimateResult describes one selectivity estimate.
type EstimateResult struct {
	Value        int  // estimated (or exact) number of matching rows
	Exact        bool // true when the count came from an index, not a sample
	Bound        int  // absolute error bound: true count is in [Value-Bound, Value+Bound]
	RowsExamined int  // rows actually inspected to produce this estimate
}

// Estimate reports how many rows would match the equality condition
// attr = value without running the query. For indexed attributes the exact
// count is read from the index (Exact, Bound 0, no rows examined). For
// other attributes a random sample of rows is inspected and the result is
// extrapolated with a Hoeffding error bound; the full table is never
// scanned. A nil value matches nothing, exactly like Query.
func (s *Store) Estimate(attr string, value any) EstimateResult {
	if value == nil {
		return EstimateResult{Value: 0, Exact: true}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := keyOf(value)
	if idx, ok := s.indexes[attr]; ok {
		return EstimateResult{Value: len(idx[key]), Exact: true}
	}
	n := len(s.order)
	if n == 0 {
		return EstimateResult{Value: 0, Exact: true}
	}
	m := sampleSize(n)
	hits := 0
	for _, i := range sampleIndices(n, m) {
		attrs := s.entities[s.order[i]]
		if v, ok := attrs[attr]; ok && v != nil && keyOf(v) == key {
			hits++
		}
	}
	est := int(math.Round(float64(hits) * float64(n) / float64(m)))
	bound := int(math.Ceil(float64(n) *
		math.Sqrt(math.Log(2/confidenceDelta)/(2*float64(m)))))
	return EstimateResult{Value: est, Bound: bound, RowsExamined: m}
}

// sampleSize picks a sample large enough for a tight bound but always far
// smaller than the table for large tables.
func sampleSize(n int) int {
	m := 4 * int(math.Ceil(math.Sqrt(float64(n))))
	if m < 64 {
		m = 64
	}
	if m > n {
		m = n
	}
	return m
}

// sampleIndices draws m distinct row positions in [0, n) without
// replacement, using a fixed seed for determinism.
func sampleIndices(n, m int) []int {
	r := rand.New(rand.NewSource(sampleSeed))
	if m*4 >= n {
		perm := r.Perm(n)
		return perm[:m]
	}
	chosen := make(map[int]struct{}, m)
	out := make([]int, 0, m)
	for len(out) < m {
		i := r.Intn(n)
		if _, dup := chosen[i]; dup {
			continue
		}
		chosen[i] = struct{}{}
		out = append(out, i)
	}
	return out
}
