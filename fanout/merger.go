package fanout

import (
	"math"
	"sort"
)

// maxUint64 is used as the "+infinity" endpoint when min has no upper bound.
const maxUint64 = ^uint64(0)

// boundsError reports that a shard violated its registered upper bounds.
type boundsError struct{ msg string }

func (e *boundsError) Error() string { return e.msg }

// checkBounds verifies a result against the shard's pre-registered bounds.
// Any violation makes the result untrusted, so the executor treats the shard
// as missing.
func checkBounds(spec ShardSpec, agg Aggregation, res ShardResult) error {
	checkValue := func(v uint64, field string) error {
		if v > spec.MaxValue {
			return &boundsError{msg: "shard " + spec.Name + ": " + field + " " +
				"exceeds registered MaxValue"}
		}
		return nil
	}
	switch agg {
	case AggCount:
		if res.Count > spec.MaxRows {
			return &boundsError{msg: "shard " + spec.Name + ": count exceeds registered MaxRows"}
		}
	case AggSum:
		// sum <= MaxRows * MaxValue, checked without overflow.
		if spec.MaxRows == 0 {
			if res.Sum != 0 {
				return &boundsError{msg: "shard " + spec.Name + ": sum exceeds MaxRows*MaxValue"}
			}
		} else {
			q, r := res.Sum/spec.MaxRows, res.Sum%spec.MaxRows
			if q > spec.MaxValue || (q == spec.MaxValue && r != 0) {
				return &boundsError{msg: "shard " + spec.Name + ": sum exceeds MaxRows*MaxValue"}
			}
		}
	case AggMin:
		return checkValue(res.Min, "min")
	case AggMax:
		return checkValue(res.Max, "max")
	case AggTopK:
		for _, p := range res.Top {
			if p.Value > spec.MaxValue {
				return &boundsError{msg: "shard " + spec.Name + ": top-k value exceeds registered MaxValue"}
			}
		}
	}
	return nil
}

// merger accumulates only successful, bound-respecting results.
type merger struct {
	req      Request
	received int
	count    uint64
	sum      uint64
	min      uint64
	hasMin   bool
	max      uint64
	hasMax   bool
	pairs    []RankedPair // merged candidate pool for top-k
}

func newMerger(req Request) *merger {
	return &merger{req: req}
}

func (m *merger) add(res ShardResult) {
	m.received++
	switch m.req.Agg {
	case AggCount:
		m.count += res.Count
	case AggSum:
		m.sum += res.Sum
	case AggMin:
		if !m.hasMin || res.Min < m.min {
			m.min = res.Min
			m.hasMin = true
		}
	case AggMax:
		if !m.hasMax || res.Max > m.max {
			m.max = res.Max
			m.hasMax = true
		}
	case AggTopK:
		for _, p := range res.Top {
			m.pairs = append(m.pairs, RankedPair{Pair: p})
		}
	}
}

// answer computes the final, order-independent Answer. missing lists exactly
// the registered specs of shards that produced no usable result.
func (m *merger) answer(missing []ShardSpec) Answer {
	ans := Answer{Agg: m.req.Agg}
	if m.received == 0 {
		// No shard succeeded: no conclusion in either direction.
		return ans
	}
	ans.Conclusive = true
	ans.Exact = len(missing) == 0

	missingRows, missingMaxValue := sumBounds(missing)

	switch m.req.Agg {
	case AggCount:
		ans.Lower, ans.Upper = m.count, addSaturating(m.count, missingRows)
		ans.HasLower, ans.HasUpper = true, true
	case AggSum:
		ans.Lower, ans.Upper = m.sum, addSaturating(m.sum, mulSaturating(missingRows, missingMaxValue))
		ans.HasLower, ans.HasUpper = true, true
	case AggMin:
		// With missing shards we only know the true minimum is no greater than
		// the smallest observed minimum, so no positive lower bound exists.
		// With every shard present the answer is an exact point value.
		ans.Upper = m.min
		ans.HasLower = ans.Exact
		if ans.Exact {
			ans.Lower = m.min
		}
		ans.HasUpper = true
	case AggMax:
		// Lower endpoint: the largest maximum observed; upper endpoint is that
		// value or the largest bound a missing shard could still hide.
		ans.Lower = m.max
		ans.HasLower = true
		if ans.Exact {
			ans.Upper = m.max
			ans.HasUpper = true
		} else {
			ans.Upper = maxU64(m.max, missingMaxValue)
			ans.HasUpper = true
		}
	case AggTopK:
		ans.Top = m.mergeTopK(missingMaxValue, ans.Exact)
	}
	return ans
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func addSaturating(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

func mulSaturating(a, b uint64) uint64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxUint64/b {
		return math.MaxUint64
	}
	return a * b
}

// sumBounds aggregates registered upper bounds over missing shards:
// the total row-count slack and the largest single value any missing shard
// could hold.
func sumBounds(missing []ShardSpec) (rows uint64, maxValue uint64) {
	for _, spec := range missing {
		rows = addSaturating(rows, spec.MaxRows)
		if spec.MaxValue > maxValue {
			maxValue = spec.MaxValue
		}
	}
	return rows, maxValue
}

// mergeTopK sorts the global candidate pool, takes K, and marks the certain
// prefix: a rank is certain only when its value is strictly greater than the
// largest value any missing shard could still produce.
func (m *merger) mergeTopK(missingMaxValue uint64, exact bool) []RankedPair {
	sort.SliceStable(m.pairs, func(i, j int) bool {
		if m.pairs[i].Value != m.pairs[j].Value {
			return m.pairs[i].Value > m.pairs[j].Value
		}
		return m.pairs[i].Key < m.pairs[j].Key
	})
	k := m.req.K
	if len(m.pairs) > k {
		m.pairs = m.pairs[:k]
	}
	if exact {
		for i := range m.pairs {
			m.pairs[i].Certain = true
		}
		return m.pairs
	}
	for i := range m.pairs {
		m.pairs[i].Certain = m.pairs[i].Value > missingMaxValue
	}
	return m.pairs
}
