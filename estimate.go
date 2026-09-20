package ontology

// EstimateResult describes the estimated selectivity of one equality
// condition attr = value.
type EstimateResult struct {
	// Count is the estimated (or exact) number of matching rows.
	Count int
	// Exact is true when Count comes from an equality index and is
	// therefore the precise answer.
	Exact bool
	// ErrorBound is a guaranteed absolute error bound: the true count
	// lies in [Count-ErrorBound, Count+ErrorBound]. It is 0 for exact
	// results.
	ErrorBound int
	// RowsExamined is how many entity rows the estimation inspected.
	// It is 0 for indexed attributes and at most the configured sample
	// size otherwise.
	RowsExamined int
}

// Estimate reports how many rows an equality condition attr = value
// would match, without executing the query. Indexed attributes are
// answered exactly from the index; other attributes are estimated from
// a small sample with a guaranteed error bound. A nil value never
// matches an equality condition, so it is answered exactly as zero.
func (s *Store) Estimate(attr string, value any) EstimateResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := keyOf(value)
	if !ok {
		return EstimateResult{Count: 0, Exact: true}
	}
	if ix, ok := s.indexes[attr]; ok {
		return EstimateResult{Count: len(ix.buckets[key]), Exact: true}
	}
	return s.estimateBySample(attr, key)
}

// estimateBySample inspects at most sampleSize rows and extrapolates.
// Callers must hold the read lock.
func (s *Store) estimateBySample(attr, key string) EstimateResult {
	n := len(s.entities)
	if n == 0 {
		return EstimateResult{Count: 0, Exact: true}
	}
	k := s.sampleSize
	if k > n {
		k = n
	}
	matches := 0
	examined := 0
	for _, props := range s.entities {
		if examined >= k {
			break
		}
		examined++
		v, ok := props[attr]
		if !ok {
			continue
		}
		if vk, ok := keyOf(v); ok && vk == key {
			matches++
		}
	}
	est := matches * n / examined
	// The unexamined n-examined rows contribute between 0 and
	// n-examined additional matches, so the true count lies in
	// [matches, matches+n-examined]. The bound below is the maximum
	// distance from est to either end of that interval and is
	// therefore guaranteed.
	lo := est - matches
	hi := matches + (n - examined) - est
	bound := lo
	if hi > bound {
		bound = hi
	}
	return EstimateResult{
		Count:        est,
		Exact:        examined == n,
		ErrorBound:   bound,
		RowsExamined: examined,
	}
}
