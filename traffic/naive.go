package traffic

// naiveModel is an independently written reference implementation:
// instead of jumping to exact instants it advances one fixed rational
// micro-step at a time, clipping queues at 0/capacity and evaluating
// the incident set at every step. The differential test chooses a
// step that divides all event times, so comparison against the
// event-driven engine is exact. Used only by tests.
type naiveModel struct {
	net    *Network
	vehLen *Rat
	step   *Rat
	now    *Rat

	incs   map[string]*incidentState
	queue  map[string]*Rat
	spill  map[string]bool
	sticky map[string]int
}

func newNaiveModel(n *Network, vehicleLength *Rat, step *Rat) *naiveModel {
	if step == nil || step.Sign() <= 0 {
		panic("naive step must be positive")
	}
	m := &naiveModel{
		net:    n,
		vehLen: ratCopy(vehicleLength),
		step:   ratCopy(step),
		now:    newRat(),
		incs:   make(map[string]*incidentState),
		queue:  make(map[string]*Rat),
		spill:  make(map[string]bool),
		sticky: make(map[string]int),
	}
	for id := range n.links {
		m.queue[id] = newRat()
	}
	return m
}

func (m *naiveModel) capV(id string) *Rat {
	return newRat().Quo(m.net.links[id].Length, m.vehLen)
}

// effective independently computes base capacity with max-ratio
// reduction plus the spill-restricted least fixed point.
func (m *naiveModel) effective() map[string]*Rat {
	c := make(map[string]*Rat)
	maxRed := make(map[string]*Rat)
	act := make(map[string]bool)
	for id, l := range m.net.links {
		c[id] = newRat().Set(l.Capacity)
		maxRed[id] = newRat()
	}
	for _, in := range m.incs {
		if !in.ended && in.start.Cmp(m.now) <= 0 {
			act[in.linkID] = true
			if in.ratio.Cmp(maxRed[in.linkID]) > 0 {
				maxRed[in.linkID] = ratCopy(in.ratio)
			}
		}
	}
	for id, l := range m.net.links {
		if act[id] {
			c[id] = newRat().Mul(l.Capacity, newRat().Sub(newRat().SetInt64(1), maxRed[id]))
		}
	}
	// Bellman-Ford style fixed point; finitely many rounds guarantee
	// termination even when spill-back runs around a cycle.
	for range len(m.net.links) + 1 {
		changed := false
		for b := range m.net.links {
			if !m.spill[b] {
				continue
			}
			for _, a := range m.net.upstream[b] {
				if c[b].Cmp(c[a]) < 0 {
					c[a] = ratCopy(c[b])
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return c
}

// levels independently computes minimum affected levels over spill edges.
func (m *naiveModel) levels() map[string]int {
	act := make(map[string]bool)
	for _, in := range m.incs {
		if !in.ended && in.start.Cmp(m.now) <= 0 {
			act[in.linkID] = true
		}
	}
	dist := make(map[string]int)
	var q []string
	for id := range m.net.links {
		if act[id] {
			dist[id] = 1
			q = append(q, id)
		}
	}
	for id, lv := range m.sticky {
		if m.spill[id] {
			if _, ok := dist[id]; !ok {
				dist[id] = lv
				q = append(q, id)
			}
		}
	}
	for len(q) > 0 {
		b := q[0]
		q = q[1:]
		if !m.spill[b] {
			continue
		}
		next := dist[b] + 1
		for _, a := range m.net.upstream[b] {
			if cur, ok := dist[a]; !ok || next < cur {
				dist[a] = next
				q = append(q, a)
			}
		}
	}
	return dist
}

// tick moves one micro-step. Constraints from an incident starting in
// the interval apply immediately (all scheduled starts land exactly on
// ticks by construction of the differential test); spill-back from a
// queue filling inside the step starts at the exact fill instant, so
// the step is split there using pre/post capacities, while the newly
// full queue itself is pinned at capacity for the remainder.
func (m *naiveModel) tick() {
	stepEnd := newRat().Add(m.now, m.step)
	remaining := newRat().Set(m.step)
	// Capacities at the interval start (incidents starting exactly at
	// stepEnd are activated first by runTo ordering of callers).
	c := m.effective()
	for remaining.Sign() > 0 {
		// Earliest time inside (now, now+remaining] a non-spilling
		// growing queue reaches capacity, or a spilling queue hits zero.
		var tau *Rat
		flipUp := map[string]bool{}
		flipDown := map[string]bool{}
		for id, l := range m.net.links {
			d := newRat().Sub(l.Arrival, c[id])
			q := m.queue[id]
			cv := m.capV(id)
			switch {
			case m.spill[id] && d.Sign() < 0 && q.Sign() > 0:
				dt := newRat().Quo(q, newRat().Neg(d))
				if dt.Cmp(remaining) <= 0 {
					ct := newRat().Add(m.now, dt)
					if tau == nil || ct.Cmp(tau) < 0 {
						tau = ct
					}
				}
			case !m.spill[id] && d.Sign() > 0 && q.Cmp(cv) < 0:
				dt := newRat().Quo(newRat().Sub(cv, q), d)
				if dt.Cmp(remaining) <= 0 {
					ct := newRat().Add(m.now, dt)
					if tau == nil || ct.Cmp(tau) < 0 {
						tau = ct
					}
				}
			}
		}
		var dt *Rat
		if tau != nil {
			dt = newRat().Sub(tau, m.now)
		} else {
			dt = newRat().Set(remaining)
		}
		for id, l := range m.net.links {
			d := newRat().Sub(l.Arrival, c[id])
			q := newRat().Add(m.queue[id], newRat().Mul(d, dt))
			cv := m.capV(id)
			if q.Cmp(cv) > 0 {
				q.Set(cv)
			}
			if q.Sign() < 0 {
				q.SetInt64(0)
			}
			m.queue[id] = q
			if tau != nil {
				if !m.spill[id] && q.Cmp(cv) >= 0 && d.Sign() > 0 {
					flipUp[id] = true
				}
				if m.spill[id] && q.Sign() == 0 && d.Sign() < 0 {
					flipDown[id] = true
				}
			}
		}
		m.now = newRat().Add(m.now, dt)
		remaining = newRat().Sub(remaining, dt)
		if tau == nil {
			break
		}
		for id := range flipUp {
			m.spill[id] = true
		}
		for id := range flipDown {
			m.spill[id] = false
			delete(m.sticky, id)
		}
		if len(flipUp) > 0 || len(flipDown) > 0 {
			lv := m.levels()
			for id := range m.net.links {
				if m.spill[id] && lv[id] > 0 {
					m.sticky[id] = lv[id]
				}
			}
			c = m.effective()
		}
	}
	if m.now.Cmp(stepEnd) != 0 {
		panic("naive tick overshot")
	}
	// External tie handling at the tick instant mirrors the engine: a
	// full queue that no longer overflows under the current capacity
	// does not spill.
	for id := range m.net.links {
		cv := m.capV(id)
		d := newRat().Sub(m.net.links[id].Arrival, c[id])
		if m.spill[id] && (m.queue[id].Sign() == 0 ||
			(m.queue[id].Cmp(cv) >= 0 && d.Sign() <= 0)) {
			m.spill[id] = false
			delete(m.sticky, id)
		}
	}
}

func (m *naiveModel) runTo(t *Rat) {
	if t.Cmp(m.now) < 0 {
		panic("naive runTo cannot rewind")
	}
	for m.now.Cmp(t) < 0 {
		m.tick()
	}
}

func (m *naiveModel) register(inc Incident) error {
	if inc.ID == "" || inc.LinkID == "" || inc.Start == nil || !validRatio(inc.Ratio) {
		return ErrInvalidArgument
	}
	if _, ok := m.net.links[inc.LinkID]; !ok {
		return ErrLinkNotFound
	}
	if _, dup := m.incs[inc.ID]; dup {
		return ErrInvalidArgument
	}
	m.runTo(inc.Start)
	m.incs[inc.ID] = &incidentState{
		linkID: inc.LinkID,
		start:  ratCopy(inc.Start),
		ratio:  ratCopy(inc.Ratio),
	}
	m.applyExternalChange()
	return nil
}

func (m *naiveModel) update(id string, at *Rat, ratio *Rat) error {
	if id == "" || at == nil {
		return ErrInvalidArgument
	}
	in, ok := m.incs[id]
	if !ok {
		return ErrIncidentNotFound
	}
	if in.ended {
		return ErrIncidentAlreadyEnded
	}
	if !validRatio(ratio) {
		return ErrRatioOutOfRange
	}
	m.runTo(at)
	in.ratio = ratCopy(ratio)
	m.applyExternalChange()
	return nil
}

func (m *naiveModel) clear(id string, at *Rat) error {
	if id == "" || at == nil {
		return ErrInvalidArgument
	}
	in, ok := m.incs[id]
	if !ok {
		return ErrIncidentNotFound
	}
	if in.ended {
		return ErrIncidentAlreadyEnded
	}
	m.runTo(at)
	in.ended = true
	m.applyExternalChange()
	return nil
}

// applyExternalChange mirrors the engine's tie arbitration after a
// registered/updated/cleared incident at the current instant.
func (m *naiveModel) applyExternalChange() {
	c := m.effective()
	for {
		changed := false
		lv := m.levels()
		for id, l := range m.net.links {
			cv := m.capV(id)
			d := newRat().Sub(l.Arrival, c[id])
			if !m.spill[id] && m.queue[id].Cmp(cv) >= 0 && d.Sign() > 0 {
				m.spill[id] = true
				changed = true
			}
			if m.spill[id] {
				if m.queue[id].Sign() == 0 ||
					(m.queue[id].Cmp(cv) >= 0 && d.Sign() <= 0) {
					m.spill[id] = false
					delete(m.sticky, id)
					changed = true
				}
			}
			if m.spill[id] && lv[id] > 0 {
				m.sticky[id] = lv[id]
			}
		}
		if !changed {
			return
		}
		c = m.effective()
	}
}

func (m *naiveModel) query(linkID string) LinkState {
	lv := m.levels()
	return LinkState{LinkID: linkID, Queue: ratCopy(m.queue[linkID]), Level: lv[linkID]}
}
