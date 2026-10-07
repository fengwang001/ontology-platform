package demand

import (
	"math"
	"slices"
)

// evaluateLocked runs one evaluation at the given time (the timestamp of
// the report that triggered it). Must be called with the mutex held.
//
// For every open window (end in (now, now+window], ends are absolute
// multiples of the slip) the predicted energy is
//
//	occurred + assumedPower * (end - now)  <=  contractDemand * window
//
// where occurred is fixed. Because every window constraint is linear in
// the assumed power, all of them collapse into a single scalar bound:
// the prediction is satisfied iff assumedPower <= bound. Cutting lowers
// the assumed power, restoring raises it.
func (c *Controller) evaluateLocked(now int64) ReportResult {
	bound := c.powerBoundLocked(now)
	power := c.lastPower

	if power > bound {
		return c.cutPhaseLocked(now, bound)
	}
	return c.restorePhaseLocked(now, bound)
}

// powerBoundLocked computes the tightest assumed-power limit over all
// open windows. A negative bound means the energy already occurred in
// some window alone exceeds the contract, so no cut can help.
func (c *Controller) powerBoundLocked(now int64) float64 {
	window := c.cfg.WindowSeconds
	slip := c.cfg.SlipSeconds
	limit := float64(c.cfg.ContractDemandKW) * float64(window)

	bound := math.Inf(1)
	for end := (now/slip + 1) * slip; end <= now+window; end += slip {
		occurred := c.cumNow - c.hist.cumAt(end-window)
		if b := (limit - occurred) / float64(end-now); b < bound {
			bound = b
		}
	}
	return bound
}

// cutPhaseLocked handles a predicted violation: pick the cut set and
// apply it. Candidates are connected loads that are not locked, not
// critical (minimum priority number), and past their min-on time.
func (c *Controller) cutPhaseLocked(now int64, bound float64) ReportResult {
	minPri, _ := c.reg.minPriority()
	var cands []cutCandidate
	ratedOf := make(map[int]int64)
	for id, l := range c.reg.loads {
		if !l.connected || l.locked || l.spec.Priority == minPri {
			continue
		}
		if now-l.since < l.spec.MinOnSeconds {
			continue
		}
		cands = append(cands, cutCandidate{id: id, rated: l.spec.RatedPowerKW, priority: l.spec.Priority})
		ratedOf[id] = l.spec.RatedPowerKW
	}

	var ids []int
	switch {
	case bound < 0:
		// Already-overrun window: no cut can fix it, cut everything.
		ids = candidateIDs(cands)
	default:
		if sel, ok := selectCutSet(cands, c.lastPower, bound); ok {
			ids = sel
		} else {
			ids = candidateIDs(cands)
		}
	}
	slices.Sort(ids)

	res := ReportResult{}
	var cutSum int64
	for _, id := range ids {
		l := c.reg.loads[id]
		l.connected = false
		l.since = now
		cutSum += ratedOf[id]
		res.Actions = append(res.Actions, Action{Kind: ActionCut, LoadID: id})
	}

	newPower := math.Max(0, c.lastPower-float64(cutSum))
	res.StillViolating = newPower > bound
	return res
}

// restorePhaseLocked handles a non-violating evaluation: try to restore
// disconnected loads past their min-off time, in ascending priority
// (ties: ascending ID). The first load whose restoration would violate
// stops the whole pass; later loads are not tried.
func (c *Controller) restorePhaseLocked(now int64, bound float64) ReportResult {
	var cands []*loadState
	for _, l := range c.reg.loads {
		if l.connected || now-l.since < l.spec.MinOffSeconds {
			continue
		}
		cands = append(cands, l)
	}
	slices.SortFunc(cands, func(a, b *loadState) int {
		if a.spec.Priority != b.spec.Priority {
			return a.spec.Priority - b.spec.Priority
		}
		return a.spec.ID - b.spec.ID
	})

	res := ReportResult{}
	power := c.lastPower
	for _, l := range cands {
		if power+float64(l.spec.RatedPowerKW) > bound {
			break
		}
		l.connected = true
		l.since = now
		power += float64(l.spec.RatedPowerKW)
		res.Actions = append(res.Actions, Action{Kind: ActionRestore, LoadID: l.spec.ID})
	}
	return res
}

func candidateIDs(cands []cutCandidate) []int {
	ids := make([]int, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.id)
	}
	return ids
}
