package demand

import (
	"cmp"
	"math"
	"slices"
)

// cutCandidate is a load eligible for cutting at evaluation time.
type cutCandidate struct {
	id       int
	rated    int64
	priority int
}

// selectCutSet chooses the subset of candidates to cut so that the
// assumed power max(0, currentPower-sum(rated)) drops to bound or below.
//
// Selection order (per spec):
//  1. fewest loads;
//  2. among ties, largest sum of priority numbers;
//  3. among ties, lexicographically smallest sorted ID sequence.
//
// currentPower must be greater than bound and bound must be >= 0
// (caller's responsibility). Returns ok=false when even cutting every
// candidate cannot reach the bound.
//
// Rated powers are integers, so the required reduction R translates to
// an integer threshold ceil(R) and the tie-break is solved exactly with
// two memoized dynamic programs: one for the maximum priority sum, then
// a feasibility DP driving a greedy lexicographic reconstruction.
func selectCutSet(cands []cutCandidate, currentPower, bound float64) ([]int, bool) {
	need := int64(math.Ceil(currentPower - bound))

	byPower := slices.Clone(cands)
	slices.SortFunc(byPower, func(a, b cutCandidate) int {
		if a.rated != b.rated {
			return cmp.Compare(b.rated, a.rated) // rated descending
		}
		return cmp.Compare(a.id, b.id)
	})

	var total int64
	for _, c := range byPower {
		total += c.rated
	}
	if total < need {
		return nil, false
	}

	// Minimum count: greedily taking the largest rated powers is optimal
	// (exchange argument: any solution can be swapped to larger items).
	count := 0
	var acc int64
	for _, c := range byPower {
		acc += c.rated
		count++
		if acc >= need {
			break
		}
	}

	bestPri := maxPrioritySum(byPower, count, need)
	ids := lexMinIDs(byPower, count, need, bestPri)
	slices.Sort(ids)
	return ids, true
}

// maxPrioritySum returns the maximum achievable sum of priorities when
// picking exactly count items from cands whose rated powers sum to >= need.
func maxPrioritySum(cands []cutCandidate, count int, need int64) int {
	const negInf = math.MinInt / 2
	type key struct {
		i, slots int
		need     int64
	}
	memo := make(map[key]int)
	var f func(i, slots int, need int64) int
	f = func(i, slots int, need int64) int {
		if need < 0 {
			need = 0 // all non-positive needs are equivalent
		}
		if slots == 0 {
			if need == 0 {
				return 0
			}
			return negInf
		}
		if len(cands)-i < slots {
			return negInf
		}
		k := key{i, slots, need}
		if v, ok := memo[k]; ok {
			return v
		}
		best := f(i+1, slots, need)
		if t := f(i+1, slots-1, need-cands[i].rated); t != negInf && t+cands[i].priority > best {
			best = t + cands[i].priority
		}
		memo[k] = best
		return best
	}
	return f(0, count, need)
}

// lexMinIDs reconstructs the lexicographically smallest sorted ID
// sequence among all subsets of exactly count items with rated sum
// >= need and priority sum exactly bestPri.
func lexMinIDs(cands []cutCandidate, count int, need int64, bestPri int) []int {
	byID := slices.Clone(cands)
	slices.SortFunc(byID, func(a, b cutCandidate) int {
		return cmp.Compare(a.id, b.id)
	})

	type key struct {
		i, slots, pri int
		need          int64
	}
	memo := make(map[key]bool)
	// can reports whether items byID[i:] contain a subset of exactly
	// slots items with priority sum exactly pri and rated sum >= need.
	var can func(i, slots, pri int, need int64) bool
	can = func(i, slots, pri int, need int64) bool {
		if need < 0 {
			need = 0
		}
		if slots == 0 {
			return pri == 0 && need == 0
		}
		if pri < 0 || len(byID)-i < slots {
			return false
		}
		k := key{i, slots, pri, need}
		if v, ok := memo[k]; ok {
			return v
		}
		ok := can(i+1, slots, pri, need) ||
			can(i+1, slots-1, pri-byID[i].priority, need-byID[i].rated)
		memo[k] = ok
		return ok
	}

	// Greedy: include the smallest ID whenever some completion exists.
	chosen := make([]int, 0, count)
	priLeft, needLeft, slotsLeft := bestPri, need, count
	for j := 0; j < len(byID) && slotsLeft > 0; j++ {
		it := byID[j]
		if can(j+1, slotsLeft-1, priLeft-it.priority, needLeft-it.rated) {
			chosen = append(chosen, it.id)
			priLeft -= it.priority
			needLeft -= it.rated
			slotsLeft--
		}
	}
	return chosen
}
