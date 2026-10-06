package insurance

import "sort"

// participant is one policy taking part in a single allocation layer.
type participant struct {
	policy *policyState
	indep  int64 // independent payout: min(max(loss-deductible,0), remaining)
}

// allocateLayer splits total among participants proportionally to their
// independent payouts (floored), then distributes the rounding remainder
// one unit at a time in (Effective asc, ID asc) order to participants
// whose independent payout exceeds their current share, until the
// remainder is exhausted. It returns shares aligned with the input slice.
//
// Precondition: 0 <= total <= sum of independent payouts.
// Guaranteed: each share <= its independent payout, and shares sum to total.
func allocateLayer(parts []participant, total int64) []int64 {
	shares := make([]int64, len(parts))
	if total <= 0 || len(parts) == 0 {
		return shares
	}
	var sumIndep int64
	for _, pt := range parts {
		sumIndep += pt.indep
	}
	if sumIndep <= 0 {
		return shares
	}

	// Floored proportional shares of the actual total.
	var allocated int64
	for i, pt := range parts {
		shares[i] = pt.indep * total / sumIndep
		allocated += shares[i]
	}
	remainder := total - allocated

	// Remainder order: effective day ascending, then policy ID ascending.
	order := make([]int, len(parts))
	for i := range parts {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		pa, pb := parts[order[a]].policy, parts[order[b]].policy
		if pa.Effective != pb.Effective {
			return pa.Effective < pb.Effective
		}
		return pa.ID < pb.ID
	})

	// Hand out one unit per pass to any participant whose independent
	// payout still exceeds its current share. Since remainder < len(parts)
	// a single pass always suffices, but loop literally per the rule.
	for remainder > 0 {
		progressed := false
		for _, idx := range order {
			if remainder == 0 {
				break
			}
			if parts[idx].indep > shares[idx] {
				shares[idx]++
				remainder--
				progressed = true
			}
		}
		if !progressed {
			break // unreachable when total <= sumIndep; defensive
		}
	}
	return shares
}
