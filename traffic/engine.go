package traffic

// incidentState is the mutable lifecycle of one registered incident.
type incidentState struct {
	linkID string
	start  *Rat
	ratio  *Rat
	ended  bool
}

// engine holds all mutable per-link state of one Network.
//
// State changes are driven only at their exact instants: between such
// instants every queue is a linear function of time, so no numerical
// integration is needed.
type engine struct {
	net *Network
	// vehicle length in the network's length unit
	vehLen *Rat

	now       *Rat
	incidents map[string]*incidentState

	// per-link dynamic state
	queue  map[string]*Rat // vehicles, always in [0, capacityVehicles]
	spill  map[string]bool // queue has reached full and not yet drained
	sticky map[string]int  // level retained by links draining a queue
	effCap map[string]*Rat // recomputed fixed point, flow per time
	maxRed map[string]*Rat // max active incident ratio on the link
	hasAct map[string]bool // whether the link has >=1 active incident
	level  map[string]int  // current affected level, 0 = unaffected
}

func newEngine(n *Network, vehicleLength *Rat) *engine {
	if vehicleLength == nil || vehicleLength.Sign() <= 0 {
		panic("vehicle length must be positive")
	}
	e := &engine{
		net:       n,
		vehLen:    ratCopy(vehicleLength),
		now:       newRat(), // time 0
		incidents: make(map[string]*incidentState),
		queue:     make(map[string]*Rat),
		spill:     make(map[string]bool),
		sticky:    make(map[string]int),
		effCap:    make(map[string]*Rat),
		maxRed:    make(map[string]*Rat),
		hasAct:    make(map[string]bool),
		level:     make(map[string]int),
	}
	for id := range n.links {
		e.queue[id] = newRat()
	}
	e.recompute()
	return e
}

// capacityVehicles returns the link length expressed in vehicles.
func (e *engine) capacityVehicles(id string) *Rat {
	l := e.net.links[id]
	return newRat().Quo(l.Length, e.vehLen)
}

// recompute rebuilds, from scratch and at a single instant:
//   - maxRed/hasAct: strongest active incident per link (max, never sum)
//   - effCap: least fixed point of
//     eff[a] = capBase(a) * (1-maxRed(a)), then
//     eff[a] = min(eff[a], eff[b]) for every spilling downstream b
//   - level: multi-source shortest (spill-restricted) distance from
//     active incidents, seeded by sticky levels of draining spill links.
//
// The fixed point relaxations run on the finite value lattice of link
// capacities: each strict relaxation strictly lowers a value drawn from
// a finite set, so cycles terminate and cannot double-apply a
// restriction (a relaxation that does not lower anything is a no-op).
func (e *engine) recompute() {
	for id, l := range e.net.links {
		base := newRat().Set(l.Capacity)
		e.maxRed[id] = newRat()
		e.hasAct[id] = false
		e.effCap[id] = base
	}
	for _, inc := range e.incidents {
		if inc.ended || inc.start.Cmp(e.now) > 0 {
			continue
		}
		id := inc.linkID
		e.hasAct[id] = true
		if inc.ratio.Cmp(e.maxRed[id]) > 0 {
			e.maxRed[id] = ratCopy(inc.ratio)
		}
	}
	for id, l := range e.net.links {
		if e.hasAct[id] {
			one := newRat().SetInt64(1)
			factor := newRat().Sub(one, e.maxRed[id])
			e.effCap[id] = newRat().Mul(l.Capacity, factor)
		}
	}

	// SPFA-style relaxation: a spilling link b limits each direct
	// upstream a to effCap[b]. Only spill links propagate constraints,
	// exactly matching "回溢只沿上游方向传播".
	queue := make([]string, 0, len(e.net.links))
	inQueue := make(map[string]bool)
	for id := range e.net.links {
		if e.spill[id] {
			queue = append(queue, id)
			inQueue[id] = true
		}
	}
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		inQueue[b] = false
		cb := e.effCap[b]
		for _, a := range e.net.upstream[b] {
			if cb.Cmp(e.effCap[a]) < 0 {
				e.effCap[a] = newRat().Set(cb)
				// Only a link whose own queue is full passes the
				// restriction further upstream; a merely restricted
				// link does not yet block its predecessors.
				if e.spill[a] && !inQueue[a] {
					inQueue[a] = true
					queue = append(queue, a)
				}
			}
		}
	}

	e.recomputeLevels()
}

// recomputeLevels assigns the minimum level over all downstream paths.
// An edge a -> b can raise a's level only when b is currently spilling
// (that is precisely when b restricts a). Seeds are active-incident
// links at level 1 and previously-leveled spill links still draining.
func (e *engine) recomputeLevels() {
	dist := make(map[string]int)
	var q []string
	// Fixed seeds at level 1 take precedence over sticky seeds.
	for id := range e.net.links {
		if e.hasAct[id] {
			dist[id] = 1
			q = append(q, id)
		}
	}
	for id, lv := range e.sticky {
		if !e.spill[id] {
			continue
		}
		if _, ok := dist[id]; !ok {
			dist[id] = lv
			q = append(q, id)
		}
	}
	for len(q) > 0 {
		b := q[0]
		q = q[1:]
		if !e.spill[b] {
			continue
		}
		next := dist[b] + 1
		for _, a := range e.net.upstream[b] {
			if cur, ok := dist[a]; !ok || next < cur {
				dist[a] = next
				q = append(q, a)
			}
		}
	}
	e.level = dist
	// Keep sticky levels aligned with the current assignment while a
	// link remains full: a link carrying its own level-1 incident must
	// record 1 so an older larger value cannot reappear when the
	// incident clears while the queue is still draining.
	for id := range e.net.links {
		if e.spill[id] {
			if lv := e.level[id]; lv > 0 {
				e.sticky[id] = lv
			}
		}
	}
}

// drift returns arrival - effective capacity for a link.
func (e *engine) drift(id string) *Rat {
	return newRat().Sub(e.net.links[id].Arrival, e.effCap[id])
}

// reconcileSpill applies the boundary rules at the current instant
// against the *current* capacities. It is used both after an internal
// boundary step and after an external change landing on a boundary
// instant: a link exactly at capacity only spills while it would keep
// overflowing, and a spill link at zero stops spilling.
func (e *engine) reconcileSpill() bool {
	changed := false
	for id := range e.net.links {
		capV := e.capacityVehicles(id)
		q := e.queue[id]
		if !e.spill[id] && q.Cmp(capV) >= 0 && e.drift(id).Sign() > 0 {
			e.queue[id] = newRat().Set(capV)
			e.spill[id] = true
			changed = true
		}
		if e.spill[id] {
			switch {
			case q.Sign() == 0:
				e.spill[id] = false
				delete(e.sticky, id)
				changed = true
			case q.Cmp(capV) >= 0 && e.drift(id).Sign() <= 0:
				// Exactly full at an external-change instant but no
				// longer overflowing: the spill never takes effect.
				e.spill[id] = false
				delete(e.sticky, id)
				changed = true
			}
		}
	}
	if changed {
		for id := range e.net.links {
			if e.spill[id] {
				if lv := e.level[id]; lv > 0 {
					e.sticky[id] = lv
				}
			}
		}
	}
	return changed
}

// applyExternalChange runs a registered/updated/cleared incident at
// the current instant and resolves any boundary ties against the new
// capacities. Repeats while the spill set keeps moving.
func (e *engine) applyExternalChange() {
	e.recompute()
	for e.reconcileSpill() {
		e.recompute()
	}
}

// advance moves event-driven to time t (>= now). All changes at the
// same instant are applied together, so ties are resolved by physical
// simultaneity rather than registration order.
func (e *engine) advance(t *Rat) {
	if t.Cmp(e.now) < 0 {
		panic("engine advance cannot rewind")
	}
	for t.Cmp(e.now) > 0 {
		// Earliest future instant at which any queue hits a boundary.
		var tNext *Rat
		for _, inc := range e.incidents {
			if inc.ended {
				continue
			}
			if inc.start.Cmp(e.now) > 0 && (tNext == nil || inc.start.Cmp(tNext) < 0) {
				tNext = ratCopy(inc.start)
			}
		}
		for id := range e.net.links {
			d := e.drift(id)
			if d.Sign() == 0 {
				continue
			}
			q := e.queue[id]
			capV := e.capacityVehicles(id)
			if e.spill[id] {
				if d.Sign() < 0 {
					// draining full queue: time to reach zero
					dt := newRat().Quo(q, newRat().Neg(d))
					cand := newRat().Add(e.now, dt)
					if tNext == nil || cand.Cmp(tNext) < 0 {
						tNext = cand
					}
				}
				continue
			}
			if d.Sign() > 0 {
				if q.Cmp(capV) >= 0 {
					continue // already full; should be spilling
				}
				dt := newRat().Quo(newRat().Sub(capV, q), d)
				cand := newRat().Add(e.now, dt)
				if tNext == nil || cand.Cmp(tNext) < 0 {
					tNext = cand
				}
			} else if q.Sign() > 0 {
				dt := newRat().Quo(q, newRat().Neg(d))
				cand := newRat().Add(e.now, dt)
				if tNext == nil || cand.Cmp(tNext) < 0 {
					tNext = cand
				}
			}
		}

		stepTo := t
		if tNext != nil && tNext.Cmp(stepTo) < 0 {
			stepTo = tNext
		}
		dt := newRat().Sub(stepTo, e.now)
		// Integrate every queue linearly over [now, stepTo].
		for id := range e.net.links {
			d := e.drift(id)
			if d.Sign() == 0 {
				continue
			}
			q := newRat().Add(e.queue[id], newRat().Mul(d, dt))
			capV := e.capacityVehicles(id)
			if e.spill[id] {
				// full queue stays pinned while arrival still exceeds
				// capacity; it can only drain downward.
				if q.Cmp(capV) > 0 {
					q.Set(capV)
				}
			} else {
				if q.Cmp(capV) > 0 {
					q.Set(capV)
				}
				if q.Sign() < 0 {
					q.SetInt64(0)
				}
			}
			e.queue[id] = q
		}
		e.now = stepTo

		// Boundary flips at this instant use the capacities in force
		// here; future incident starts are activated by recompute.
		reachedStart := false
		for _, inc := range e.incidents {
			if !inc.ended && inc.start.Cmp(e.now) == 0 {
				reachedStart = true
			}
		}
		if e.reconcileSpill() || reachedStart {
			e.recompute()
			for e.reconcileSpill() {
				e.recompute()
			}
		}

		if e.now.Cmp(t) >= 0 {
			break
		}
	}
}
