package ontology

import "math"

// sampleTarget caps how many rows an estimation touches.
const sampleTarget = 512

// delta is the failure probability of the sampling error bound.
const delta = 1e-6

// Estimate is the result of estimating the selectivity of one
// equality condition attr = value.
type Estimate struct {
	// Rows is the estimated (or exact) number of matching rows.
	Rows int
	// Exact is true when Rows comes from an equality index and is
	// therefore the precise count, with AbsErrorBound == 0.
	Exact bool
	// AbsErrorBound bounds the absolute error of a sampled estimate:
	// the true count lies in [Rows-Bound, Rows+Bound] with
	// probability at least 1-1e-6. Zero when Exact.
	AbsErrorBound int
	// RowsChecked reports how many rows the estimation inspected.
	// It is 0 for indexed attributes and at most sampleTarget for
	// sampled ones, always far below the total row count.
	RowsChecked int
}

// Estimate returns the selectivity of attr = value without executing
// the query. Indexed attributes are answered exactly from the index;
// other attributes are answered from a deterministic sample of at
// most sampleTarget rows, never a full table scan.
func (s *Store) Estimate(attr string, value any) Estimate {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := keyOf(value)
	if !ok {
		// Equality with nil matches nothing by definition.
		return Estimate{Rows: 0, Exact: true}
	}
	if s.attrSet[attr] {
		return Estimate{Rows: len(s.index[attr][key]), Exact: true}
	}
	return s.sampleEstimate(attr, key)
}

// sampleEstimate estimates the hit count for an unindexed attribute
// from a strided sample of the ID list.
func (s *Store) sampleEstimate(attr, key string) Estimate {
	total := len(s.ids)
	if total == 0 {
		return Estimate{Rows: 0, Exact: true}
	}
	n := min(total, sampleTarget)
	step := float64(total) / float64(n)

	hits := 0
	for i := 0; i < n; i++ {
		id := s.ids[int(float64(i)*step)]
		v, ok := s.ents[id][attr]
		if !ok || v == nil {
			continue
		}
		if k, _ := keyOf(v); k == key {
			hits++
		}
	}

	est := float64(hits) * float64(total) / float64(n)
	// Hoeffding: |est - true| <= total * sqrt(ln(2/delta) / (2n))
	// with probability at least 1-delta.
	bound := float64(total) * math.Sqrt(math.Log(2/delta)/(2*float64(n)))
	return Estimate{
		Rows:          int(math.Round(est)),
		Exact:         false,
		AbsErrorBound: int(math.Ceil(bound)),
		RowsChecked:   n,
	}
}
