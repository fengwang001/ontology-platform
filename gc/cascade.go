package gc

import "time"

// engine drives one operation to its unique stable state. It is created by
// the controller for a single mutating call, fed with the directly affected
// objects, and then converges the graph by repeatedly applying the rules in
// a canonical order:
//
//  1. foreground propagation, to a fixpoint;
//  2. one pass of removals (which may enqueue more work);
//
// repeating until neither queue has anything left. Propagation always runs
// before removal, which is what makes the outcome independent of the order
// in which individual objects happen to be visited (see docs/DESIGN.md).
//
// All work is queued per affected object, so the cost of convergence is
// proportional to the cascade actually touched, never to the total number
// of objects in the store.
type engine struct {
	s     *store
	now   time.Time
	emit  func(Event)
	stats *Stats

	propQ map[string]struct{}
	remQ  map[string]struct{}
}

func newEngine(s *store, now time.Time, emit func(Event), stats *Stats) *engine {
	return &engine{
		s:     s,
		now:   now,
		emit:  emit,
		stats: stats,
		propQ: make(map[string]struct{}),
		remQ:  make(map[string]struct{}),
	}
}

func (e *engine) converge() {
	for len(e.propQ) > 0 || len(e.remQ) > 0 {
		for len(e.propQ) > 0 {
			id := pop(e.propQ)
			o := e.s.objs[id]
			if o == nil {
				continue
			}
			e.checkPropagation(o)
		}
		for len(e.remQ) > 0 {
			id := pop(e.remQ)
			o := e.s.objs[id]
			if o == nil {
				continue
			}
			e.tryRemove(o)
		}
	}
}

func pop(set map[string]struct{}) string {
	for id := range set {
		delete(set, id)
		return id
	}
	return ""
}

// markDeleting puts o into the deleting state with the given policy, or
// upgrades a background deletion to foreground. Every other combination is
// a no-op, which makes repeated deletes idempotent.
func (e *engine) markDeleting(o *Object, p Policy) {
	if o.Deleting {
		if o.Policy == PolicyBackground && p == PolicyForeground {
			o.Policy = PolicyForeground
			e.emit(Event{Kind: EventUpgradeForeground, ID: o.ID, Policy: p})
			e.remQ[o.ID] = struct{}{}
			e.enqueueDependents(o)
		}
		return
	}
	o.Deleting = true
	o.Policy = p
	o.DeletionRequestedAt = e.now
	e.emit(Event{Kind: EventMarkDeleting, ID: o.ID, Policy: p})
	e.remQ[o.ID] = struct{}{}
	e.enqueueDependents(o)
	// o is no longer live: its blocking references stop counting against
	// its owners, which may open their foreground deletion gate.
	for _, ref := range o.Owners {
		e.stats.EdgesWalked++
		if ref.Block {
			e.s.blocking[ref.ID]--
			e.remQ[ref.ID] = struct{}{}
		}
	}
}

func (e *engine) enqueueDependents(o *Object) {
	for _, depID := range e.s.dependentIDs(o.ID) {
		e.propQ[depID] = struct{}{}
		e.stats.EdgesWalked++
	}
}

// checkPropagation applies the foreground propagation rule to o: if o is
// alive (or deleting in background) and every owner of o is gone or
// deleting, with at least one of them deleting in foreground, then o joins
// the deletion in foreground. The rule applies to blocking and
// non-blocking dependents alike.
func (e *engine) checkPropagation(o *Object) {
	e.stats.PropagationChecks++
	if o.Deleting && o.Policy != PolicyBackground {
		return
	}
	if len(o.Owners) == 0 {
		return
	}
	hasForegroundOwner := false
	for _, ref := range o.Owners {
		e.stats.EdgesWalked++
		owner := e.s.objs[ref.ID]
		if owner == nil {
			continue // gone owners count as satisfied
		}
		if !owner.Deleting {
			return
		}
		if owner.Policy == PolicyForeground {
			hasForegroundOwner = true
		}
	}
	if hasForegroundOwner {
		e.markDeleting(o, PolicyForeground)
	}
}

// tryRemove physically removes o when every removal condition holds:
// deleting, no finalizers, and — for foreground policy — no live blocking
// dependent. Removal detaches all references in both directions and may
// cascade background deletion into dependents that lost their last owner.
func (e *engine) tryRemove(o *Object) {
	e.stats.RemovalChecks++
	if !o.Deleting || len(o.Finalizers) > 0 {
		return
	}
	if o.Policy == PolicyForeground && e.s.blocking[o.ID] > 0 {
		return
	}
	e.emit(Event{Kind: EventRemove, ID: o.ID})

	for _, depID := range e.s.dependentIDs(o.ID) {
		e.stats.EdgesWalked++
		dep := e.s.objs[depID]
		if dep == nil {
			continue
		}
		e.s.detach(dep, o.ID)
		e.emit(Event{Kind: EventDetach, ID: dep.ID, Owner: o.ID})
		if dep.Deleting {
			continue
		}
		if o.Policy != PolicyOrphan && len(dep.Owners) == 0 {
			e.markDeleting(dep, PolicyBackground)
		} else {
			// The dependent stays alive, but its propagation
			// eligibility may have changed.
			e.propQ[dep.ID] = struct{}{}
		}
	}

	for _, ref := range o.Owners {
		e.stats.EdgesWalked++
		e.s.removeEdge(o.ID, ref)
	}
	delete(e.s.objs, o.ID)
	delete(e.s.blocking, o.ID)
}
