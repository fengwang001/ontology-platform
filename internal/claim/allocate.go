package claim

import (
	"math/big"
	"sort"
)

// allocationCandidate is one policy taking part in a sharing round.
type allocationCandidate struct {
	PolicyID string
	StartDay int
	Capacity int64
}

// shareGroup distributes a capped total among candidates by capacity
// proportions, floor first, then one unit at a time in (StartDay, PolicyID)
// order, never exceeding a candidate's capacity.
//
// Rules implemented:
//   - base[i] = floor(pot * cap[i] / sum)
//   - remainder units are handed out one at a time, cycling through
//     candidates in (StartDay, PolicyID) order and skipping any candidate
//     that has already reached its capacity, until the pot is exhausted.
func shareGroup(candidates []allocationCandidate, pot int64) map[string]int64 {
	out := make(map[string]int64, len(candidates))
	if len(candidates) == 0 || pot <= 0 {
		return out
	}

	eligible := make([]int, 0, len(candidates))
	var sum int64
	for i, c := range candidates {
		out[c.PolicyID] = 0
		if c.Capacity > 0 {
			eligible = append(eligible, i)
			sum += c.Capacity
		}
	}
	if sum <= 0 {
		return out
	}
	if pot > sum {
		pot = sum
	}

	sort.Slice(eligible, func(a, b int) bool {
		x, y := candidates[eligible[a]], candidates[eligible[b]]
		if x.StartDay != y.StartDay {
			return x.StartDay < y.StartDay
		}
		return x.PolicyID < y.PolicyID
	})

	// Floor shares via arbitrary precision: pot*capacity can overflow int64.
	bigPot := big.NewInt(pot)
	var distributed int64
	for _, idx := range eligible {
		c := candidates[idx]
		prod := new(big.Int).Mul(bigPot, big.NewInt(c.Capacity))
		base := prod.Quo(prod, big.NewInt(sum)).Int64()
		if base > c.Capacity {
			base = c.Capacity
		}
		out[c.PolicyID] = base
		distributed += base
	}
	remaining := pot - distributed

	// Doubly linked cyclic list over eligible, in key order. A candidate is
	// unlinked the moment it is full, so every visited node receives a unit
	// and each round makes progress.
	n := len(eligible)
	prev := make([]int, n)
	next := make([]int, n)
	for i := range eligible {
		prev[i] = (i - 1 + n) % n
		next[i] = (i + 1) % n
	}

	cur := 0
	for remaining > 0 && n > 0 {
		c := candidates[eligible[cur]]
		out[c.PolicyID]++
		remaining--

		if out[c.PolicyID] >= c.Capacity {
			n--
			if n > 0 {
				p, q := prev[cur], next[cur]
				next[p] = q
				prev[q] = p
				cur = q
				continue
			}
		}
		cur = next[cur]
	}

	return out
}
