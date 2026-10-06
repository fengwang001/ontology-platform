package signal

// engine is the deterministic core. All times are integer seconds.
//
// Time model: the state only changes at discrete events (phase-end,
// serving-completion). advanceTo replays events up to t; when the state is
// "clean" (no priority activity, no pending plan) and sits exactly at a
// cycle wrap with phase 0, whole cycles are skipped in O(1) because the
// per-cycle resync adjustment is constant until the deviation resolves.
type engine struct {
	specs []PhaseSpec
	n     int

	plan     Plan
	cycle    int
	pending  *Plan // accepted, takes effect at next cycle wrap
	replaced *Plan // last pending plan silently replaced before taking effect

	anchor  int // planned cycle starts are anchor+plan.Offset+k*cycle
	devR    int // raw deviation in [0, cycle), measured at the last wrap
	adjMag  int // total green adjustment applied to the current traversal
	adjDir  int // +1 lengthen, -1 shorten, 0 none
	travLen int // duration of the current traversal (sum greens+clearances)

	phase      int
	phaseStart int
	greenEnd   int // end of current green; clearance runs until greenEnd+clearance
	greens     []int
	cycleIdx   int
	atWrap     bool // phase 0 of a standard (undisturbed) traversal just started

	serving  *Request
	queue    []*Request // emergency requests waiting, ordered by (Time, ID)
	requests map[string]*Request

	skipped     map[int]map[int]bool // cycleIdx -> phases skipped by jumps
	pendingJump int                  // phase to jump to at current phase end, -1 none
	jumpHold    bool                 // jump target should be held at MaxGreen

	steps int64 // processed transitions; instrumentation for complexity proofs
}

func newEngine(specs []PhaseSpec, plan Plan, t int) (*engine, error) {
	if err := validateSpecs(specs); err != nil {
		return nil, err
	}
	cycle, err := validatePlan(specs, plan)
	if err != nil {
		return nil, err
	}
	greens := append([]int(nil), plan.Greens...)
	e := &engine{
		specs:       append([]PhaseSpec(nil), specs...),
		n:           len(specs),
		plan:        plan,
		cycle:       cycle,
		anchor:      t - plan.Offset,
		greens:      greens,
		travLen:     cycle,
		phaseStart:  t,
		greenEnd:    t + greens[0],
		atWrap:      true,
		requests:    map[string]*Request{},
		skipped:     map[int]map[int]bool{},
		pendingJump: -1,
	}
	return e, nil
}

func (e *engine) clone() *engine {
	c := *e
	c.greens = append([]int(nil), e.greens...)
	if e.pending != nil {
		p := *e.pending
		p.Greens = append([]int(nil), e.pending.Greens...)
		c.pending = &p
	}
	if e.replaced != nil {
		p := *e.replaced
		p.Greens = append([]int(nil), e.replaced.Greens...)
		c.replaced = &p
	}
	c.requests = make(map[string]*Request, len(e.requests))
	for id, r := range e.requests {
		rc := *r
		c.requests[id] = &rc
	}
	c.queue = make([]*Request, len(e.queue))
	for i, r := range e.queue {
		c.queue[i] = c.requests[r.ID]
	}
	if e.serving != nil {
		c.serving = c.requests[e.serving.ID]
	}
	c.skipped = make(map[int]map[int]bool, len(e.skipped))
	for k, v := range e.skipped {
		nv := make(map[int]bool, len(v))
		for p, b := range v {
			nv[p] = b
		}
		c.skipped[k] = nv
	}
	return &c
}

func (e *engine) phaseEnd() int {
	return e.greenEnd + e.specs[e.phase].Clearance
}

func (e *engine) nextEvent() int {
	if e.serving != nil && e.pendingJump < 0 && e.greenEnd < e.phaseEnd() {
		return e.greenEnd
	}
	return e.phaseEnd()
}

func (e *engine) clean() bool {
	return e.serving == nil && len(e.queue) == 0 && e.pending == nil && e.pendingJump < 0
}

// advanceTo replays all events with time <= t. Cost is O(phases + queued
// requests) plus O(1) per skipped run of clean cycles, independent of the
// number of elapsed cycles.
func (e *engine) advanceTo(t int) {
	for {
		if e.atWrap && e.phase == 0 && e.clean() && e.skipCycles(t) {
			continue
		}
		ev := e.nextEvent()
		if ev > t {
			return
		}
		e.doEvent()
	}
}

func (e *engine) doEvent() {
	e.steps++
	ev := e.nextEvent()
	if e.serving != nil && e.pendingJump < 0 && ev == e.greenEnd {
		r := e.serving
		r.State = StateCompleted
		e.serving = nil
		e.pumpQueue(ev)
		return
	}
	// Phase transition at ev.
	e.completeBuses()
	wrapped := false
	var next int
	hold := false
	isJump := e.pendingJump >= 0
	if e.pendingJump >= 0 {
		next = e.pendingJump
		e.pendingJump = -1
		hold = e.jumpHold
		e.jumpHold = false
	} else {
		next = (e.phase + 1) % e.n
	}
	if next <= e.phase {
		e.wrap(ev)
		wrapped = true
	}
	e.startPhase(next, ev)
	if hold {
		e.greenEnd = ev + e.specs[next].MaxGreen
	}
	e.atWrap = wrapped && !isJump
}

func (e *engine) startPhase(p, t int) {
	e.phase = p
	e.phaseStart = t
	e.greenEnd = t + e.greens[p]
}

// wrap processes a cycle boundary at time t: a pending plan takes effect,
// otherwise the deviation is measured and greens are readjusted.
func (e *engine) wrap(t int) {
	e.cycleIdx++
	if e.pending != nil {
		e.plan = *e.pending
		e.pending = nil
		e.cycle = planCycle(e.specs, e.plan)
		e.anchor = t - e.plan.Offset
		e.devR = 0
	} else {
		e.devR = modPos(t-e.anchor-e.plan.Offset, e.cycle)
	}
	e.greens, e.adjMag, e.adjDir = resyncGreens(e.specs, e.plan, e.devR)
	e.travLen = clearanceSum(e.specs)
	for _, g := range e.greens {
		e.travLen += g
	}
	for c := range e.skipped {
		if c < e.cycleIdx-1 {
			delete(e.skipped, c)
		}
	}
}

// resyncGreens derives the green vector for a new traversal from the raw
// deviation r: shorten when 0 < r < cycle/2, lengthen when r >= cycle/2
// (half-cycle tie lengthens).
func resyncGreens(specs []PhaseSpec, p Plan, r int) (greens []int, mag, dir int) {
	cycle := planCycle(specs, p)
	if r == 0 {
		return append([]int(nil), p.Greens...), 0, 0
	}
	g, m := adjustedGreens(specs, p, r)
	if 2*r < cycle {
		return g, m, -1
	}
	return g, m, +1
}

// skipCycles fast-forwards whole clean cycles starting at a phase-0 wrap.
// Precondition: atWrap && phase == 0 && clean(). It advances to the last
// wrap time <= t and reports whether any cycle was skipped.
func (e *engine) skipCycles(t int) bool {
	c := e.cycle
	if e.adjMag == 0 {
		k := (t - e.phaseStart) / c
		if k <= 0 {
			return false
		}
		e.commitSkip(k, int64(k)*int64(c))
		return true
	}
	m := e.adjMag
	var d int // remaining deviation magnitude
	if e.adjDir < 0 {
		d = e.devR
	} else {
		d = c - e.devR
	}
	n := (d + m - 1) / m // traversals until deviation resolves
	last := d - (n-1)*m
	unit := c + e.adjDir*m
	stable := (n-1)*unit + c + e.adjDir*last
	elapsed := t - e.phaseStart
	if elapsed >= stable {
		extra := (elapsed - stable) / c
		total := n + extra
		e.devR = 0
		e.adjMag, e.adjDir = 0, 0
		e.greens = append([]int(nil), e.plan.Greens...)
		e.travLen = c
		e.commitSkip(total, int64(stable)+int64(extra)*int64(c))
		return true
	}
	k := elapsed / unit
	if k > n-1 {
		// The final traversal has length c+dir*last, which for a shorten
		// run exceeds unit; cap so we never skip past its start.
		k = n - 1
	}
	if k <= 0 {
		return false
	}
	if e.adjDir < 0 {
		e.devR -= k * m
	} else {
		e.devR = modPos(e.devR+k*m, c)
	}
	e.greens, e.adjMag, e.adjDir = resyncGreens(e.specs, e.plan, e.devR)
	e.travLen = clearanceSum(e.specs)
	for _, g := range e.greens {
		e.travLen += g
	}
	e.commitSkip(k, int64(k)*int64(unit))
	return true
}

func (e *engine) commitSkip(k int, dt int64) {
	e.cycleIdx += k
	e.phaseStart += int(dt)
	e.greenEnd = e.phaseStart + e.greens[0]
	// Every skipped traversal contains phase transitions, at which applied
	// bus requests complete naturally.
	e.completeBuses()
	for cyc := range e.skipped {
		if cyc < e.cycleIdx-1 {
			delete(e.skipped, cyc)
		}
	}
}

// query reports the observable state at t without mutating the engine.
func (e *engine) query(t int) QueryResult {
	c := e.clone()
	c.advanceTo(t)
	dev := c.devR
	if 2*dev > c.cycle {
		dev -= c.cycle
	}
	return QueryResult{
		Phase:     c.phase,
		Elapsed:   t - c.phaseStart,
		Remaining: c.phaseEnd() - t,
		Deviation: dev,
	}
}
