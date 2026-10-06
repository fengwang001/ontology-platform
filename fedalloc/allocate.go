package fedalloc

import (
	"math/big"
	"sort"
)

// allocate computes exact target replica counts for an immutable snapshot.
// It is a pure function: it never mutates registry state.
func allocate(views []clusterView, total int64) ([]int64, *AllocationError) {
	targets := make([]int64, len(views))
	if total < 0 {
		return nil, newError(KindInvalidArgument, "total replicas must not be negative")
	}

	// Phase 1: minimum guarantees. Unavailable clusters are always zero and
	// their current replicas must migrate (handled by the change plan).
	sumMin := new(big.Int)
	for i := range views {
		v := &views[i]
		if !v.available {
			continue
		}
		if v.minReplicas > v.cap {
			return nil, newError(KindConfigConflict,
				"cluster "+v.name+": minReplicas "+itoa(v.minReplicas)+
					" exceeds effective cap "+itoa(v.cap))
		}
		targets[i] = v.minReplicas
		sumMin.Add(sumMin, big.NewInt(v.minReplicas))
	}
	if sumMin.Cmp(big.NewInt(total)) > 0 {
		return nil, newError(KindMinExceedsTotal,
			"sum of minimum replicas "+sumMin.String()+" exceeds requested total "+itoa(total))
	}

	remainder := new(big.Int).Sub(big.NewInt(total), sumMin)

	// Participants: available clusters with positive weight. Zero-weight
	// available clusters keep exactly their minimum.
	type participant struct {
		idx       int
		saturated bool
	}
	parts := make([]*participant, 0, len(views))
	for i := range views {
		v := &views[i]
		if v.available && v.weight > 0 {
			p := &participant{idx: i}
			// Already at the effective cap after the minimum: saturated up front.
			if targets[i] >= v.cap {
				p.saturated = true
			}
			parts = append(parts, p)
		}
	}

	// Phase 2: repeated weight-proportional sharing rounds among unsaturated
	// participants. Each round: proportional floors, largest-remainder +1
	// bonuses, and clusters whose floor reaches their cap are pinned and
	// removed; their overflow rejoins the next round.
	for remainder.Sign() > 0 {
		active := make([]*participant, 0, len(parts))
		weightSum := new(big.Int)
		for _, p := range parts {
			if !p.saturated {
				active = append(active, p)
				weightSum.Add(weightSum, big.NewInt(views[p.idx].weight))
			}
		}
		if len(active) == 0 {
			return nil, newError(KindInsufficientCapacity,
				"all eligible clusters saturated; short by "+remainder.String()+" replicas")
		}

		type roundRow struct {
			p         *participant
			fractionN *big.Int // numerator of the fractional part, denominator weightSum
		}
		rows := make([]roundRow, 0, len(active))
		assigned := new(big.Int)

		for _, p := range active {
			v := &views[p.idx]
			headroom := v.cap - targets[p.idx]
			if headroom <= 0 {
				p.saturated = true
				continue
			}
			product := new(big.Int).Mul(remainder, big.NewInt(v.weight))
			q, m := new(big.Int).QuoRem(product, weightSum, new(big.Int))
			floor := q.Int64()
			if floor >= headroom {
				// Pin to the cap; the overflow re-enters the next round.
				targets[p.idx] = v.cap
				p.saturated = true
				assigned.Add(assigned, big.NewInt(headroom))
				continue
			}
			targets[p.idx] += floor
			assigned.Add(assigned, big.NewInt(floor))
			rows = append(rows, roundRow{p: p, fractionN: m})
		}

		left := new(big.Int).Sub(remainder, assigned)

		// Largest-remainder bonuses use this round's fixed fractional ordering
		// (fraction desc; on tie current load desc; then name asc). Pinned
		// clusters carry no row, so they are simply skipped; their floor
		// overflow is already part of `left` and rejoins the next round over
		// the survivors. Current load is consulted only on exact ties.
		sort.Slice(rows, func(a, b int) bool {
			cmp := rows[a].fractionN.Cmp(rows[b].fractionN)
			if cmp != 0 {
				return cmp > 0
			}
			ca := views[rows[a].p.idx].currentReplicas
			cb := views[rows[b].p.idx].currentReplicas
			if ca != cb {
				return ca > cb
			}
			return views[rows[a].p.idx].name < views[rows[b].p.idx].name
		})

		for _, row := range rows {
			if left.Sign() == 0 {
				break
			}
			v := &views[row.p.idx]
			if targets[row.p.idx] >= v.cap {
				continue
			}
			targets[row.p.idx]++
			left.Sub(left, big.NewInt(1))
			if targets[row.p.idx] == v.cap {
				// A bonus filled the last headroom: stop the round and
				// redistribute what is left among the survivors.
				row.p.saturated = true
				break
			}
		}

		remainder = left
	}

	return targets, nil
}

func itoa(v int64) string { return big.NewInt(v).String() }
