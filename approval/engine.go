package approval

import "ontology/deadline"

// virtualView returns the state of r as it would be after expiries up to now.
// It mutates nothing.
func virtualView(r *request, t, now int64) (pos int, ta int64, outcome Outcome, finalAt int64) {
	if r.outcome != Pending {
		return r.pos, r.ta, r.outcome, r.finalAt
	}
	pos, ta = r.pos, r.ta
	n := len(r.candidates)
	if now >= ta+t {
		k := (now - ta) / t // full periods elapsed
		if k >= int64(n-pos) {
			// Last candidate expires at ta + (n-pos)*t.
			finalAt = ta + int64(n-pos)*t
			return n - 1, finalAt - t, Expired, finalAt
		}
		pos += int(k)
		ta += k * t
	}
	return pos, ta, Pending, 0
}

// processDue drives the heap for all requests due by now, advancing
// candidates and finalizing Expired ones. Caller holds the write lock.
func (e *Engine) processDue(now int64) {
	e.examined = 0
	for {
		top, ok := e.heap.Peek()
		if !ok || top.DueAt > now {
			if ok {
				e.examined++
			}
			return
		}
		e.examined++
		r := e.reqs[top.Req]
		e.heap.Pop()
		// Advance every level already due; each level costs t and the
		// new assignment time is the deadline, not now.
		for r.outcome == Pending && r.ta+e.t <= now {
			r.pos++
			r.ta += e.t
			if r.pos >= len(r.candidates) {
				r.outcome = Expired
				r.finalAt = r.ta
				r.pos = len(r.candidates) - 1
				r.ta -= e.t
				break
			}
		}
		if r.outcome == Pending {
			e.heap.Push(deadline.Item{Req: top.Req, DueAt: r.ta + e.t})
		}
	}
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Submit freezes the approval chain computed from the org as of now.
func (e *Engine) Submit(req, a string, amount, now int64) error {
	if req == "" || a == "" || amount < 1 || amount > maxAmount || !validNow(now) {
		return ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.clock {
		return ErrClock
	}
	if _, exists := e.reqs[req]; exists {
		return ErrExist
	}

	// Freeze candidates: walk a's manager chain (excluding a) in order,
	// keeping members whose current limit is at least amount.
	var candidates []string
	seen := map[string]bool{a: true}
	m, ok := e.org.Manager(a)
	for ok {
		if seen[m] { // org guards cycles; stay defensive for a fresh walk
			break
		}
		seen[m] = true
		if e.org.Limit(m) >= amount {
			candidates = append(candidates, m)
		}
		m, ok = e.org.Manager(m)
	}
	if len(candidates) == 0 {
		return ErrNoApprover
	}

	// Validation passed: apply expiries virtually (no new request yet),
	// commit the request, then advance the clock.
	e.processDue(now)
	e.reqs[req] = &request{
		amount:     amount,
		candidates: candidates,
		pos:        0,
		ta:         now,
		outcome:    Pending,
	}
	e.heap.Push(deadline.Item{Req: req, DueAt: now + e.t})
	e.clock = now
	return nil
}
