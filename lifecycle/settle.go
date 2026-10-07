package lifecycle

import (
	"fmt"
	"time"
)

// settleRun holds the state of one access-triggered settlement. Everything
// runs under the engine mutex, which is also what makes concurrent accesses
// serialize: the second caller observes already-advanced state and settles
// zero rings, so no expiry ring can fire twice.
type settleRun struct {
	now    time.Time
	caller string
	rings  []SettleRecord

	// order records the declared chain order of visited instances.
	order []string

	// forcedDone marks instances whose forced-wave trigger (the single
	// expiry ring that starts a chain, plus its depth-first forced
	// cascade) has already been processed this run.
	forcedDone map[string]bool

	// expiryDone marks instances whose own full expiry-ring loop has run.
	expiryDone map[string]bool

	// dueChecks counts time-span judgments ("due?") performed this run.
	dueChecks int

	regression *Failure // category-4 anomaly, remembered until the end
}

// settleAll lazily settles id and the chain closure it belongs to. It runs
// under e.mu. Declared predecessors are settled even when the call entered
// the chain through a middle instance.
//
// Ordering model (two waves over the topological closure):
//
//	wave 1 - forced: each instance fires at most the single already-due
//	         expiry ring leaving its current state, then immediately
//	         depth-fires its chained forced effects, which continue the
//	         same depth-first walk. Produces e.g. a-expire, b-force,
//	         c-force before anyone continues past their trigger ring.
//	wave 2 - expiry: each instance advances through the remaining due
//	         expiry rings of its own chain; rings fired here trigger
//	         further wave-1 cascades first.
func (e *Engine) settleAll(id string, now time.Time, caller string) (*settleRun, *Failure) {
	run := &settleRun{
		now:        now,
		caller:     caller,
		forcedDone: map[string]bool{},
		expiryDone: map[string]bool{},
	}

	closure, ferr := e.closureLocked(id)
	if ferr != nil {
		return run, ferr
	}

	for _, node := range closure {
		if ferr := e.settleForced(node, run); ferr != nil {
			return run, ferr
		}
	}
	for _, node := range closure {
		if ferr := e.settleExpiry(node, run); ferr != nil {
			return run, ferr
		}
	}
	return run, run.regression
}

// noteRegression records a clock-regression observation (lowest priority)
// without aborting the settlement.
func (e *Engine) noteRegression(in *inst, run *settleRun) {
	if !run.now.Before(in.highWater) {
		in.highWater = maxTime(in.highWater, run.now)
		return
	}
	if run.regression == nil {
		run.regression = fail(ErrClockRegression,
			"instance %q observed %s after previously observing %s",
			in.id, run.now.Format(time.RFC3339Nano), in.highWater.Format(time.RFC3339Nano))
	}
	in.highWater = maxTime(in.highWater, run.now)
}

// settleForced runs wave 1 for one instance: observe the clock, then fire
// the single due trigger ring (if any) and cascade its forced effects.
func (e *Engine) settleForced(in *inst, run *settleRun) *Failure {
	if run.forcedDone[in.id] || run.expiryDone[in.id] {
		return nil
	}
	run.forcedDone[in.id] = true
	if !contains(run.order, in.id) {
		run.order = append(run.order, in.id)
	}
	e.noteRegression(in, run)

	def, due := expiryTransition(e.typeOf(in), in.state)
	if !due {
		return nil
	}
	dueAt := in.entered.Add(def.Duration)
	if run.now.Before(dueAt) {
		return nil // not yet due; wave 2 has nothing more to do either
	}
	run.dueChecks++
	if ferr := e.checkExpiryGuard(in, def, dueAt, run); ferr != nil {
		return ferr
	}
	e.fireExpiry(in, def, dueAt, run)
	return e.cascadeForced(in, def, dueAt, run)
}

// settleExpiry runs wave 2 for one instance: every subsequent due expiry
// ring of its own chain, cascading forced effects between rings.
func (e *Engine) settleExpiry(in *inst, run *settleRun) *Failure {
	if run.expiryDone[in.id] {
		return nil
	}
	run.expiryDone[in.id] = true
	if !contains(run.order, in.id) {
		run.order = append(run.order, in.id)
	}

	for {
		def, due := expiryTransition(e.typeOf(in), in.state)
		if !due {
			return nil
		}
		dueAt := in.entered.Add(def.Duration)
		if run.now.Before(dueAt) {
			return nil
		}
		run.dueChecks++
		if ferr := e.checkExpiryGuard(in, def, dueAt, run); ferr != nil {
			return ferr
		}
		e.fireExpiry(in, def, dueAt, run)
		if ferr := e.cascadeForced(in, def, dueAt, run); ferr != nil {
			return ferr
		}
	}
}

// checkExpiryGuard evaluates a due ring's optional non-temporal guard.
func (e *Engine) checkExpiryGuard(in *inst, def TransitionDef, dueAt time.Time, run *settleRun) *Failure {
	if def.Guard == nil {
		return nil
	}
	if def.Guard.Allowed(e.evalContext(in, run.now)) {
		return nil
	}
	return fail(ErrExpiryGuard,
		"instance %q expiry transition %q (%s -> %s) due at %s but guard failed",
		in.id, def.Name, def.From, def.To, dueAt.Format(time.RFC3339Nano))
}

// fireExpiry commits one expiry ring. The new state is entered at the
// deterministic due instant (not observation time), so delayed settlement
// does not shift the next ring's clock source.
func (e *Engine) fireExpiry(in *inst, def TransitionDef, dueAt time.Time, run *settleRun) {
	prev := in.state
	in.state = def.To
	in.entered = dueAt
	in.version++
	e.appendEvent(in, Event{
		At:         run.now,
		OccurredAt: dueAt,
		Kind:       EventExpiry,
		State:      def.To,
		Prev:       prev,
		Transition: def.Name,
		Detail: fmt.Sprintf("entered %s + duration %s = due %s, observed %s",
			prev, def.Duration, dueAt.Format(time.RFC3339Nano),
			run.now.Format(time.RFC3339Nano)),
	})
	run.rings = append(run.rings, SettleRecord{
		Instance:   in.id,
		Transition: def.Name,
		From:       prev,
		To:         def.To,
		Basis: fmt.Sprintf("expiry due at %s (observed %s)",
			dueAt.Format(time.RFC3339Nano), run.now.Format(time.RFC3339Nano)),
	})
}

// cascadeForced applies a fired ring's chained effects in declared order,
// depth-first: each forced target transitions immediately, then its own
// forced effects continue before control returns.
func (e *Engine) cascadeForced(src *inst, fired TransitionDef, at time.Time, run *settleRun) *Failure {
	for _, ch := range fired.Chain {
		targetID, linked := src.links[ch.Link]
		if !linked {
			return fail(ErrChainTrigger,
				"transition %q of instance %q: link %q is not bound",
				fired.Name, src.id, ch.Link)
		}
		target, ferr := e.mustGetLocked(targetID)
		if ferr != nil {
			return ferr
		}
		t := e.typeOf(target)
		def, ok := forcedTransition(t, ch.TransitionName)
		if !ok {
			return fail(ErrChainTrigger,
				"target %q (type %q) has no forced transition %q",
				target.id, t.Name, ch.TransitionName)
		}
		if target.state != ch.TargetFrom || def.From != ch.TargetFrom {
			return fail(ErrChainTrigger,
				"chained effect %q expects target %q in state %q but it is %q",
				ch.TransitionName, target.id, ch.TargetFrom, target.state)
		}
		if ch.Guard != nil && !ch.Guard.Allowed(e.evalContext(target, at)) {
			return fail(ErrChainTrigger,
				"chained effect %q on instance %q rejected by its guard",
				ch.TransitionName, target.id)
		}

		e.commitForced(target, def, at,
			fmt.Sprintf("forced by %q.%s via link %q", src.id, fired.Name, ch.Link))
		run.rings = append(run.rings, SettleRecord{
			Instance:   target.id,
			Transition: def.Name,
			From:       def.From,
			To:         def.To,
			Basis:      fmt.Sprintf("chained from %q.%s at %s", src.id, fired.Name, at.Format(time.RFC3339Nano)),
		})

		// Forced arrival invalidates any earlier wave markers so its
		// declared chain continues depth-first, then its own expiry rings
		// run afterwards in wave 2.
		delete(run.forcedDone, target.id)
		delete(run.expiryDone, target.id)
		if !contains(run.order, target.id) {
			run.order = append(run.order, target.id)
		}
		if ferr := e.cascadeForced(target, def, at, run); ferr != nil {
			return ferr
		}
	}
	return nil
}

// commitForced records a chained effect on a target instance.
func (e *Engine) commitForced(target *inst, def TransitionDef, at time.Time, detail string) {
	prev := target.state
	target.state = def.To
	target.entered = at
	target.version++
	target.highWater = maxTime(target.highWater, at)
	e.appendEvent(target, Event{
		At:         at,
		OccurredAt: at,
		Kind:       EventForced,
		State:      def.To,
		Prev:       prev,
		Transition: def.Name,
		Detail:     detail,
	})
}

// closureLocked computes the chain-connected component (following outgoing
// links and indexed backlinks) and orders it by declared chain direction.
func (e *Engine) closureLocked(seed string) ([]*inst, *Failure) {
	start, err := e.mustGetLocked(seed)
	if err != nil {
		return nil, err
	}

	visited := map[string]bool{start.id: true}
	var nodes []*inst
	var walk func(in *inst)
	walk = func(in *inst) {
		nodes = append(nodes, in)
		t := e.typeOf(in)
		for _, d := range t.Transitions {
			for _, ch := range d.Chain {
				if target, ok := in.links[ch.Link]; ok {
					if tin, ok := e.insts[target]; ok && !visited[tin.id] {
						visited[tin.id] = true
						walk(tin)
					}
				}
			}
		}
		for _, edge := range e.backlinks[in.id] {
			if src, ok := e.insts[edge.source]; ok && !visited[src.id] {
				visited[src.id] = true
				walk(src)
			}
		}
	}
	walk(start)

	// Kahn ordering. A cyclic chained-effect topology makes declared order
	// undefined and is a category-2 failure.
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, in := range nodes {
		indeg[in.id] += 0
		t := e.typeOf(in)
		for _, d := range t.Transitions {
			for _, ch := range d.Chain {
				if target, ok := in.links[ch.Link]; ok && visited[target] {
					adj[in.id] = append(adj[in.id], target)
					indeg[target]++
				}
			}
		}
	}

	ready := make(instQueue, 0, len(nodes))
	for _, in := range nodes {
		if indeg[in.id] == 0 {
			ready.push(in)
		}
	}
	var ordered []*inst
	for len(ready) > 0 {
		in := ready.pop()
		ordered = append(ordered, in)
		for _, next := range adj[in.id] {
			indeg[next]--
			if indeg[next] == 0 {
				ready.push(e.insts[next])
			}
		}
	}
	if len(ordered) != len(nodes) {
		return nil, fail(ErrChainTrigger, "cyclic chained-effect topology involving instance %q", seed)
	}
	return ordered, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// instQueue is a small FIFO for the topological ordering.
type instQueue []*inst

func (q *instQueue) push(in *inst) { *q = append(*q, in) }
func (q *instQueue) pop() *inst {
	in := (*q)[0]
	*q = (*q)[1:]
	return in
}
