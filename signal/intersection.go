package signal

import "sync"

// Intersection is the concurrent facade. Every mutating operation runs on a
// cloned engine and is committed only when fully validated, so rejected
// operations leave no trace on state, queues, or the operation clock.
// Concurrent calls are serialized by a mutex, hence linearizable; replaying
// the same accepted sequence yields identical history.
type Intersection struct {
	mu     sync.Mutex
	e      *engine
	lastOp int
}

// NewIntersection builds an intersection with fixed phase specs and an
// initial feasible plan, starting at startTime.
func NewIntersection(specs []PhaseSpec, plan Plan, startTime int) (*Intersection, error) {
	if startTime < 0 {
		return nil, ErrInvalidParam
	}
	e, err := newEngine(specs, plan, startTime)
	if err != nil {
		return nil, err
	}
	return &Intersection{e: e, lastOp: startTime}, nil
}

// ChangePlan accepts a new timing plan. It takes effect at the end of the
// current cycle; a previously accepted but not yet effective plan is
// silently replaced (queryable via PendingPlan).
func (x *Intersection) ChangePlan(p Plan, t int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if t < 0 {
		return ErrInvalidParam
	}
	if t < x.lastOp {
		return ErrClockRollback
	}
	if _, err := validatePlan(x.e.specs, p); err != nil {
		return err
	}
	c := x.e.clone()
	c.advanceTo(t)
	pc := Plan{Greens: append([]int(nil), p.Greens...), Offset: p.Offset, MaxAdjustPerCycle: p.MaxAdjustPerCycle}
	if c.pending != nil {
		c.replaced = c.pending
	}
	c.pending = &pc
	x.e = c
	x.lastOp = t
	return nil
}

// RequestEmergency registers an emergency priority request for target.
func (x *Intersection) RequestEmergency(id string, target, t int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if id == "" || t < 0 {
		return ErrInvalidParam
	}
	if t < x.lastOp {
		return ErrClockRollback
	}
	if target < 0 || target >= x.e.n {
		return ErrPhaseNotFound
	}
	if _, dup := x.e.requests[id]; dup {
		return ErrDuplicateRequest
	}
	c := x.e.clone()
	c.advanceTo(t)
	sk := computeSkips(c.phase, target, c.cycleIdx, c.n)
	if violatesSkips(c.skipped, sk) {
		return ErrConsecutiveSkip
	}
	if c.serving != nil && c.serving.Target == target {
		return ErrTargetOccupied
	}
	for _, q := range c.queue {
		if q.Target == target {
			return ErrTargetOccupied
		}
	}
	registerSkips(c.skipped, sk)
	r := &Request{ID: id, Kind: ReqEmergency, Target: target, Time: t, State: StateQueued}
	c.requests[id] = r
	i := len(c.queue)
	for i > 0 {
		q := c.queue[i-1]
		if q.Time < t || (q.Time == t && q.ID < id) {
			break
		}
		i--
	}
	c.queue = append(c.queue, nil)
	copy(c.queue[i+1:], c.queue[i:])
	c.queue[i] = r
	c.pumpQueue(t)
	c.advanceTo(t)
	x.e = c
	x.lastOp = t
	return nil
}

// ConfirmPassage reports that the emergency vehicle of a serving request
// has passed; the held target phase ends now. Unknown or inactive IDs are
// ignored (the call still counts as an accepted operation for the clock).
func (x *Intersection) ConfirmPassage(id string, t int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if id == "" || t < 0 {
		return ErrInvalidParam
	}
	if t < x.lastOp {
		return ErrClockRollback
	}
	c := x.e.clone()
	c.advanceTo(t)
	if c.serving != nil && c.serving.ID == id {
		r := c.serving
		r.State = StateCompleted
		c.serving = nil
		if c.pendingJump >= 0 {
			c.jumpHold = false
		} else {
			c.greenEnd = t
			c.atWrap = false
		}
		c.pumpQueue(t)
		c.advanceTo(t)
	}
	x.e = c
	x.lastOp = t
	return nil
}

// RequestBus registers a bus priority request: extend or shorten the
// current green by delta seconds, clamped to [MinGreen, MaxGreen]. It never
// reorders the emergency queue; when an emergency request is active or
// queued the bus request is accepted but recorded as preempted.
func (x *Intersection) RequestBus(id string, kind BusKind, delta, t int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if id == "" || t < 0 || delta <= 0 || (kind != BusExtend && kind != BusShorten) {
		return ErrInvalidParam
	}
	if t < x.lastOp {
		return ErrClockRollback
	}
	if _, dup := x.e.requests[id]; dup {
		return ErrDuplicateRequest
	}
	c := x.e.clone()
	c.advanceTo(t)
	r := &Request{ID: id, Kind: ReqBus, Bus: kind, Delta: delta, Time: t, State: StateApplied}
	switch {
	case c.serving != nil || len(c.queue) > 0:
		r.State = StatePreempted
	case t < c.greenEnd:
		if kind == BusExtend {
			ge := c.greenEnd + delta
			if mx := c.phaseStart + c.specs[c.phase].MaxGreen; ge > mx {
				ge = mx
			}
			c.greenEnd = ge
		} else {
			ge := c.greenEnd - delta
			if mn := c.phaseStart + c.specs[c.phase].MinGreen; ge < mn {
				ge = mn
			}
			if ge < t {
				ge = t
			}
			c.greenEnd = ge
		}
		c.atWrap = false
	}
	c.requests[id] = r
	c.advanceTo(t)
	x.e = c
	x.lastOp = t
	return nil
}

// Query reports the phase, elapsed/remaining seconds, and signed offset
// deviation at time t. It is read-only and its cost does not grow with the
// number of elapsed cycles.
func (x *Intersection) Query(t int) QueryResult {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.e.query(t)
}

// RequestState returns the state of a known request as of time t.
func (x *Intersection) RequestState(id string, t int) (RequestState, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	c := x.e.clone()
	c.advanceTo(t)
	r, ok := c.requests[id]
	if !ok {
		return "", false
	}
	return r.State, true
}

// PendingPlan returns the accepted-but-not-yet-effective plan and the last
// pending plan it silently replaced, if any.
func (x *Intersection) PendingPlan() (pending, replaced *Plan) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e.pending != nil {
		p := *x.e.pending
		p.Greens = append([]int(nil), x.e.pending.Greens...)
		pending = &p
	}
	if x.e.replaced != nil {
		p := *x.e.replaced
		p.Greens = append([]int(nil), x.e.replaced.Greens...)
		replaced = &p
	}
	return
}
