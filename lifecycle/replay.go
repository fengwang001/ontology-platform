package lifecycle

import (
	"sort"
	"time"
)

// History returns the committed event log of an instance (oldest first).
func (e *Engine) History(id string) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return nil, ferr
	}
	out := make([]Event, len(in.history))
	copy(out, in.history)
	return out, nil
}

// StateAt derives the state an instance would have been observed in at
// instant t if a lazy-settlement read had happened exactly then. It folds
// committed history up to t into a throwaway engine and runs one
// hypothetical settlement at t; the current live settled state is never
// consulted, so the answer is independent of present-day settlement.
func (e *Engine) StateAt(id string, t time.Time) (Snapshot, error) {
	sim, dueChecks, err := e.buildSimulation(t)
	if err != nil {
		return Snapshot{}, err
	}
	_ = dueChecks
	sim.mu.Lock()
	defer sim.mu.Unlock()
	in, ferr := sim.mustGetLocked(id)
	if ferr != nil {
		return Snapshot{}, ferr
	}
	return sim.snapshotLocked(in, t), simErr(sim, id)
}

func simErr(sim *Engine, id string) error {
	// Errors during hypothetical settlement are stored on the simulation
	// keyed by first failing instance.
	if err, ok := sim.simErrs[id]; ok {
		return err
	}
	return sim.simErrFirst
}

// ReplayDueChecksAt reports how many expiry-due probes the hypothetical
// settlement at t performed. The cost-proof test asserts this number is
// bounded by the number of rings newly settled, never by the length of the
// instance's full history.
func (e *Engine) ReplayDueChecksAt(t time.Time) (int, error) {
	_, n, err := e.buildSimulation(t)
	return n, err
}

func (e *Engine) buildSimulation(t time.Time) (*Engine, int, error) {
	e.mu.Lock()
	type timedEvent struct {
		id    string
		ev    Event
		order int
	}
	var all []timedEvent
	idx := 0
	for _, id := range e.sortedInstanceIDsLocked() {
		in := e.insts[id]
		for _, ev := range in.history {
			if ev.At.After(t) {
				break
			}
			all = append(all, timedEvent{id: id, ev: ev, order: idx})
			idx++
		}
	}
	typesCopy := e.types
	e.mu.Unlock()

	// Chronological order. Ties (a chained forced ring shares its source's
	// due instant) keep the order in which they were committed, which is
	// exactly the declared chain order.
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].ev.At.Equal(all[j].ev.At) {
			return all[i].ev.At.Before(all[j].ev.At)
		}
		return all[i].order < all[j].order
	})

	sim := NewEngine(func() time.Time { return t }, nil)
	sim.types = typesCopy
	sim.simErrs = map[string]error{}

	for _, te := range all {
		if ferr := sim.foldEvent(te.id, te.ev); ferr != nil {
			return nil, 0, ferr
		}
	}

	dueChecks := 0
	settled := map[string]bool{}
	for _, id := range sim.sortedInstanceIDsLocked() {
		if settled[id] {
			continue
		}
		run, ferr := sim.settleAll(id, t, "StateAt")
		dueChecks += run.dueChecks
		for _, done := range run.order {
			settled[done] = true
		}
		if ferr != nil {
			if _, seen := sim.simErrs[id]; !seen {
				sim.simErrs[id] = ferr
			}
			if sim.simErrFirst == nil || HigherPriority(ferr, sim.simErrFirst) {
				sim.simErrFirst = ferr
			}
		}
	}
	return sim, dueChecks, nil
}

// foldEvent rebuilds one committed event into the simulation without
// triggering any settlement itself.
func (e *Engine) foldEvent(id string, ev Event) *Failure {
	if ev.Kind == EventInitial {
		in := &inst{
			id:        id,
			typeName:  ev.TypeName,
			state:     ev.State,
			entered:   ev.At,
			highWater: ev.At,
			props:     map[string]string{},
			links:     map[string]string{},
			seq:       1,
			history:   []Event{ev},
		}
		e.insts[id] = in
		return nil
	}

	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return ferr
	}
	in.seq++
	in.history = append(in.history, ev)
	switch ev.Kind {
	case EventExpiry:
		in.state = ev.State
		in.entered = ev.OccurredAt
		in.highWater = maxTime(in.highWater, ev.At)
		in.version++
	case EventForced:
		in.state = ev.State
		in.entered = ev.OccurredAt
		in.highWater = maxTime(in.highWater, ev.At)
		in.version++
	case EventAction:
		in.state = ev.State
		in.entered = ev.OccurredAt
		in.version++
	case EventProperty:
		in.props[ev.Name] = ev.Value
		in.version++
	case EventLink:
		if old, had := in.links[ev.Name]; had {
			e.removeBacklinkLocked(id, ev.Name, old)
		}
		in.links[ev.Name] = ev.Value
		e.backlinks[ev.Value] = append(e.backlinks[ev.Value],
			chainEdge{source: id, link: ev.Name, target: ev.Value})
		in.version++
	}
	return nil
}
