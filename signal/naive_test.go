package signal

// naive is an independent, deliberately simple per-second simulation used
// to cross-check the event-driven engine. It shares only the pure spec
// formulas (planCycle, modPos, resyncGreens, skip-set helpers); all time
// advancement is done by ticking one second at a time.

type naiveReq struct {
	id     string
	kind   ReqKind
	target int
	bus    BusKind
	delta  int
	time   int
	state  RequestState
}

type naive struct {
	specs []PhaseSpec
	plan  Plan
	cyc   int

	pending  *Plan
	replaced *Plan
	anchor   int
	devR     int
	greens   []int

	phase      int
	phaseStart int
	greenEnd   int
	cycleIdx   int

	serving *naiveReq
	queue   []*naiveReq
	reqs    map[string]*naiveReq
	skipped map[int]map[int]bool
	pJump   int
	jHold   bool

	now    int
	lastOp int
}

func newNaive(specs []PhaseSpec, plan Plan, t int) *naive {
	greens := append([]int(nil), plan.Greens...)
	return &naive{
		specs:      append([]PhaseSpec(nil), specs...),
		plan:       plan,
		cyc:        planCycle(specs, plan),
		anchor:     t - plan.Offset,
		greens:     greens,
		phaseStart: t,
		greenEnd:   t + greens[0],
		reqs:       map[string]*naiveReq{},
		skipped:    map[int]map[int]bool{},
		pJump:      -1,
		now:        t,
		lastOp:     t,
	}
}

func (m *naive) clone() *naive {
	c := *m
	c.greens = append([]int(nil), m.greens...)
	if m.pending != nil {
		p := *m.pending
		p.Greens = append([]int(nil), m.pending.Greens...)
		c.pending = &p
	}
	if m.replaced != nil {
		p := *m.replaced
		p.Greens = append([]int(nil), m.replaced.Greens...)
		c.replaced = &p
	}
	c.reqs = make(map[string]*naiveReq, len(m.reqs))
	for id, r := range m.reqs {
		rc := *r
		c.reqs[id] = &rc
	}
	c.queue = make([]*naiveReq, len(m.queue))
	for i, r := range m.queue {
		c.queue[i] = c.reqs[r.id]
	}
	if m.serving != nil {
		c.serving = c.reqs[m.serving.id]
	}
	c.skipped = make(map[int]map[int]bool, len(m.skipped))
	for k, v := range m.skipped {
		nv := make(map[int]bool, len(v))
		for p, b := range v {
			nv[p] = b
		}
		c.skipped[k] = nv
	}
	return &c
}

// settle processes every event scheduled exactly at m.now.
func (m *naive) settle() {
	for {
		if m.serving != nil && m.pJump < 0 && m.now == m.greenEnd {
			r := m.serving
			r.state = StateCompleted
			m.serving = nil
			m.pump()
			continue
		}
		if m.now == m.greenEnd+m.specs[m.phase].Clearance {
			m.transition()
			continue
		}
		break
	}
}

func (m *naive) transition() {
	for _, r := range m.reqs {
		if r.kind == ReqBus && r.state == StateApplied {
			r.state = StateCompleted
		}
	}
	var next int
	hold := false
	if m.pJump >= 0 {
		next = m.pJump
		m.pJump = -1
		hold = m.jHold
		m.jHold = false
	} else {
		next = (m.phase + 1) % len(m.specs)
	}
	if next <= m.phase {
		m.wrap()
	}
	m.phase = next
	m.phaseStart = m.now
	m.greenEnd = m.now + m.greens[next]
	if hold {
		m.greenEnd = m.now + m.specs[next].MaxGreen
	}
}

func (m *naive) wrap() {
	m.cycleIdx++
	if m.pending != nil {
		m.plan = *m.pending
		m.pending = nil
		m.cyc = planCycle(m.specs, m.plan)
		m.anchor = m.now - m.plan.Offset
		m.devR = 0
	} else {
		m.devR = modPos(m.now-m.anchor-m.plan.Offset, m.cyc)
	}
	g, _, _ := resyncGreens(m.specs, m.plan, m.devR)
	m.greens = g
	for c := range m.skipped {
		if c < m.cycleIdx-1 {
			delete(m.skipped, c)
		}
	}
}

func (m *naive) pump() {
	for m.serving == nil && len(m.queue) > 0 {
		r := m.queue[0]
		m.queue = m.queue[1:]
		m.startService(r)
	}
}

func (m *naive) startService(r *naiveReq) {
	for _, q := range m.reqs {
		if q.kind == ReqBus && q.state == StateApplied {
			q.state = StatePreempted
		}
	}
	r.state = StateServing
	m.serving = r
	p := m.phase
	if p == r.target {
		ge := m.phaseStart + m.specs[p].MaxGreen
		if ge < m.now {
			ge = m.now
		}
		m.greenEnd = ge
		return
	}
	e1 := m.phaseStart + m.specs[p].MinGreen
	if e1 < m.now {
		e1 = m.now
	}
	m.greenEnd = e1
	m.pJump = r.target
	m.jHold = true
}

func (m *naive) advanceTo(t int) {
	for m.now < t {
		m.now++
		m.settle()
	}
}

func (m *naive) changePlan(p Plan, t int) error {
	if t < 0 {
		return ErrInvalidParam
	}
	if t < m.lastOp {
		return ErrClockRollback
	}
	if _, err := validatePlan(m.specs, p); err != nil {
		return err
	}
	w := m.clone()
	w.advanceTo(t)
	pc := Plan{Greens: append([]int(nil), p.Greens...), Offset: p.Offset, MaxAdjustPerCycle: p.MaxAdjustPerCycle}
	if w.pending != nil {
		w.replaced = w.pending
	}
	w.pending = &pc
	w.lastOp = t
	*m = *w
	return nil
}

func (m *naive) requestEmergency(id string, target, t int) error {
	if id == "" || t < 0 {
		return ErrInvalidParam
	}
	if t < m.lastOp {
		return ErrClockRollback
	}
	if target < 0 || target >= len(m.specs) {
		return ErrPhaseNotFound
	}
	if _, dup := m.reqs[id]; dup {
		return ErrDuplicateRequest
	}
	w := m.clone()
	w.advanceTo(t)
	sk := computeSkips(w.phase, target, w.cycleIdx, len(w.specs))
	if violatesSkips(w.skipped, sk) {
		return ErrConsecutiveSkip
	}
	if w.serving != nil && w.serving.target == target {
		return ErrTargetOccupied
	}
	for _, q := range w.queue {
		if q.target == target {
			return ErrTargetOccupied
		}
	}
	registerSkips(w.skipped, sk)
	r := &naiveReq{id: id, kind: ReqEmergency, target: target, time: t, state: StateQueued}
	w.reqs[id] = r
	i := len(w.queue)
	for i > 0 {
		q := w.queue[i-1]
		if q.time < t || (q.time == t && q.id < id) {
			break
		}
		i--
	}
	w.queue = append(w.queue, nil)
	copy(w.queue[i+1:], w.queue[i:])
	w.queue[i] = r
	w.pump()
	w.settle()
	w.lastOp = t
	*m = *w
	return nil
}

func (m *naive) confirm(id string, t int) error {
	if id == "" || t < 0 {
		return ErrInvalidParam
	}
	if t < m.lastOp {
		return ErrClockRollback
	}
	w := m.clone()
	w.advanceTo(t)
	if w.serving != nil && w.serving.id == id {
		r := w.serving
		r.state = StateCompleted
		w.serving = nil
		if w.pJump >= 0 {
			w.jHold = false
		} else {
			w.greenEnd = t
		}
		w.pump()
		w.settle()
	}
	w.lastOp = t
	*m = *w
	return nil
}

func (m *naive) requestBus(id string, kind BusKind, delta, t int) error {
	if id == "" || t < 0 || delta <= 0 || (kind != BusExtend && kind != BusShorten) {
		return ErrInvalidParam
	}
	if t < m.lastOp {
		return ErrClockRollback
	}
	if _, dup := m.reqs[id]; dup {
		return ErrDuplicateRequest
	}
	w := m.clone()
	w.advanceTo(t)
	r := &naiveReq{id: id, kind: ReqBus, bus: kind, delta: delta, time: t, state: StateApplied}
	switch {
	case w.serving != nil || len(w.queue) > 0:
		r.state = StatePreempted
	case t < w.greenEnd:
		if kind == BusExtend {
			ge := w.greenEnd + delta
			if mx := w.phaseStart + w.specs[w.phase].MaxGreen; ge > mx {
				ge = mx
			}
			w.greenEnd = ge
		} else {
			ge := w.greenEnd - delta
			if mn := w.phaseStart + w.specs[w.phase].MinGreen; ge < mn {
				ge = mn
			}
			if ge < t {
				ge = t
			}
			w.greenEnd = ge
		}
	}
	w.reqs[id] = r
	w.settle()
	w.lastOp = t
	*m = *w
	return nil
}

func (m *naive) query(t int) QueryResult {
	w := m.clone()
	w.advanceTo(t)
	dev := w.devR
	if 2*dev > w.cyc {
		dev -= w.cyc
	}
	return QueryResult{
		Phase:     w.phase,
		Elapsed:   t - w.phaseStart,
		Remaining: w.greenEnd + w.specs[w.phase].Clearance - t,
		Deviation: dev,
	}
}

func (m *naive) requestState(id string, t int) (RequestState, bool) {
	w := m.clone()
	w.advanceTo(t)
	r, ok := w.reqs[id]
	if !ok {
		return "", false
	}
	return r.state, true
}
