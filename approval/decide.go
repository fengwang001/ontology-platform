package approval

// Decide records who's decision at now. The assignee is checked after
// virtual expiry processing; approval authority is re-validated against
// the org at the decision moment.
func (e *Engine) Decide(req, who string, ok bool, now int64) error {
	if req == "" || who == "" || !validNow(now) {
		return ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.clock {
		return ErrClock
	}
	r, exists := e.reqs[req]
	if !exists {
		return ErrNotFound
	}

	// Compute the virtual post-expiry state without mutating anything,
	// so a rejected operation leaves clock, heap and request untouched.
	pos, _, outcome, _ := virtualView(r, e.t, now)
	if outcome != Pending {
		return ErrClosed
	}
	assignee := r.candidates[pos]
	if who != assignee {
		return ErrNotAssignee
	}
	// Authority is checked against the *current* org, not the frozen chain.
	if e.org.Limit(who) < r.amount {
		return ErrRevoked
	}

	// Commit: apply due expiries, then the terminal decision at now.
	e.processDue(now)
	e.heap.Delete(req)
	if ok {
		r.outcome = Approved
	} else {
		r.outcome = Rejected
	}
	r.finalAt = now
	e.clock = now
	return nil
}

// Status returns the state after virtual expiry processing at now.
// It does not advance the clock or mutate any state.
func (e *Engine) Status(req string, now int64) (Status, error) {
	if req == "" || !validNow(now) {
		return Status{}, ErrInvalid
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if now < e.clock {
		return Status{}, ErrClock
	}
	r, exists := e.reqs[req]
	if !exists {
		return Status{}, ErrNotFound
	}

	pos, ta, outcome, finalAt := virtualView(r, e.t, now)
	s := Status{Outcome: outcome, FinalAt: finalAt}
	if outcome == Pending {
		s.Assignee = r.candidates[pos]
		s.AssignedAt = ta
	} else if outcome == Expired {
		s.Assignee = ""
		s.AssignedAt = 0
	} else {
		// Terminal Approved/Rejected: report the final assignee's frozen
		// assignment time for a fully reproducible view.
		s.Assignee = r.candidates[r.pos]
		s.AssignedAt = r.ta
	}
	return s, nil
}

// String renders an outcome for logs and tests.
func (o Outcome) String() string {
	switch o {
	case Approved:
		return "Approved"
	case Rejected:
		return "Rejected"
	case Expired:
		return "Expired"
	default:
		return "Pending"
	}
}
