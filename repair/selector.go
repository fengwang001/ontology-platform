package repair

type preemption struct {
	contractor *contractor
	victim     *ticket
}

func chooseCandidate(target *ticket, contractors map[int64]*contractor) *contractor {
	var best *contractor
	for _, candidate := range contractors {
		if !canServe(candidate, target) || len(candidate.active) >= candidate.capacity {
			continue
		}
		if best == nil || betterCandidate(candidate, best) {
			best = candidate
		}
	}
	return best
}

func choosePreemption(target *ticket, contractors map[int64]*contractor) preemption {
	var best preemption
	for _, candidate := range contractors {
		if !canServe(candidate, target) || len(candidate.active) < candidate.capacity {
			continue
		}
		victim := preemptionVictim(candidate)
		if victim == nil {
			continue
		}
		current := preemption{contractor: candidate, victim: victim}
		if best.contractor == nil || betterCandidate(candidate, best.contractor) {
			best = current
		}
	}
	return best
}

func canServe(candidate *contractor, target *ticket) bool {
	return !candidate.inactive &&
		candidate.trades[target.trade] &&
		candidate.buildings[target.building] &&
		!target.rejectedBy[candidate.id] &&
		(target.level != Urgent || candidate.acceptsUrgent)
}

func betterCandidate(left, right *contractor) bool {
	if len(left.active) != len(right.active) {
		return len(left.active) < len(right.active)
	}
	if left.lastCompletedAt != right.lastCompletedAt {
		return left.lastCompletedAt < right.lastCompletedAt
	}
	return left.registration < right.registration
}

func preemptionVictim(candidate *contractor) *ticket {
	var victim *ticket
	for _, active := range candidate.active {
		if active.status != StatusAssigned || active.level == Urgent {
			continue
		}
		if victim == nil || active.dispatchedAt > victim.dispatchedAt ||
			(active.dispatchedAt == victim.dispatchedAt && active.id > victim.id) {
			victim = active
		}
	}
	return victim
}
