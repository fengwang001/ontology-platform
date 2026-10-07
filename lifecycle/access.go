package lifecycle

import (
	"fmt"
	"time"
)

// Get reads an instance state. Every due expiry ring of the instance (and
// of its declared chain) is settled before the result is returned.
func (e *Engine) Get(id string) (Snapshot, error) {
	return e.read(id, "Get")
}

// Properties returns all property values after settlement.
func (e *Engine) Properties(id string) (map[string]string, error) {
	if _, err := e.read(id, "Properties"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return nil, ferr
	}
	out := make(map[string]string, len(in.props))
	for k, v := range in.props {
		out[k] = v
	}
	return out, nil
}

func (e *Engine) read(id, caller string) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return Snapshot{}, ferr
	}
	now := e.clock()
	run, settleErr := e.settleAll(id, now, caller)
	e.emitLog(caller, id, now, run.rings, settleErr)
	if settleErr != nil {
		return e.snapshotLocked(in, now), settleErr
	}
	return e.snapshotLocked(in, now), nil
}

// Act invokes an explicit action. All due expiry rings are settled first;
// the action's precondition is evaluated only against the post-settlement
// state. A rejected action changes nothing (state, properties or clock
// stamps); already committed rings of this call remain committed.
func (e *Engine) Act(id, action string) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return Snapshot{}, ferr
	}
	now := e.clock()

	run, settleErr := e.settleAll(id, now, "Act:"+action)
	if settleErr != nil {
		// A clock-regression anomaly (lowest priority) does not block the
		// explicit action: committed rings stay committed and the action is
		// judged normally against the current state.
		if settleErr.Category() == CategoryClockRegression {
			settleErr = nil
		}
	}
	if settleErr != nil {
		e.emitLog("Act:"+action, id, now, run.rings, settleErr)
		return e.snapshotLocked(in, now), settleErr
	}

	def, ok := actionTransition(e.typeOf(in), in.state, action)
	if !ok {
		err := fail(ErrActionPrecondition,
			"instance %q in state %q has no enabled action %q after settlement",
			id, in.state, action)
		e.emitLog("Act:"+action, id, now, run.rings, err)
		return e.snapshotLocked(in, now), err
	}
	if def.Guard != nil && !def.Guard.Allowed(e.evalContext(in, now)) {
		err := fail(ErrActionPrecondition,
			"instance %q action %q guard failed in state %q", id, action, in.state)
		e.emitLog("Act:"+action, id, now, run.rings, err)
		return e.snapshotLocked(in, now), err
	}

	prev := in.state
	in.state = def.To
	in.entered = now
	in.version++
	e.appendEvent(in, Event{
		At:         now,
		OccurredAt: now,
		Kind:       EventAction,
		State:      def.To,
		Prev:       prev,
		Transition: def.Name,
		Detail:     fmt.Sprintf("explicit action %q", action),
	})
	run.rings = append(run.rings, SettleRecord{
		Instance:   id,
		Transition: def.Name,
		From:       prev,
		To:         def.To,
		Basis:      fmt.Sprintf("explicit action %q at %s after settlement", action, now.Format(time.RFC3339Nano)),
	})

	// An action transition may itself carry chained effects, settled in the
	// same declared order as expiry-driven chains.
	if ferr := e.cascadeForced(in, def, now, run); ferr != nil {
		e.emitLog("Act:"+action, id, now, run.rings, ferr)
		return e.snapshotLocked(in, now), ferr
	}

	e.emitLog("Act:"+action, id, now, run.rings, nil)
	return e.snapshotLocked(in, now), nil
}

// SetProperty mutates a property after settlement; guard failures leave
// all instances untouched.
func (e *Engine) SetProperty(id, name, value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return ferr
	}
	now := e.clock()
	run, settleErr := e.settleAll(id, now, "SetProperty:"+name)
	if settleErr != nil {
		// Property administration is external data input, not a state read
		// or a lifecycle action: a category-1 expiry guard stop on this
		// instance must not make its own guard inputs un-writable. Other
		// failure classes still reject the write.
		if settleErr.Category() == CategoryExpiryGuard {
			settleErr = nil
		}
	}
	if settleErr != nil {
		e.emitLog("SetProperty:"+name, id, now, run.rings, settleErr)
		return settleErr
	}
	old := in.props[name]
	in.props[name] = value
	in.version++
	e.appendEvent(in, Event{
		At:         now,
		OccurredAt: now,
		Kind:       EventProperty,
		State:      in.state,
		Prev:       old,
		Name:       name,
		Value:      value,
		Detail:     fmt.Sprintf("property %q = %q", name, value),
	})
	e.emitLog("SetProperty:"+name, id, now, run.rings, nil)
	return nil
}

// SetLink binds a link slot from one instance to another after settlement.
func (e *Engine) SetLink(from, link, to string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	src, ferr := e.mustGetLocked(from)
	if ferr != nil {
		return ferr
	}
	if _, ferr := e.mustGetLocked(to); ferr != nil {
		return ferr
	}
	now := e.clock()
	run, settleErr := e.settleAll(from, now, "SetLink:"+link)
	if settleErr != nil {
		// Same external-input rule as SetProperty.
		if settleErr.Category() == CategoryExpiryGuard {
			settleErr = nil
		}
	}
	if settleErr != nil {
		e.emitLog("SetLink:"+link, from, now, run.rings, settleErr)
		return settleErr
	}

	if old, ok := src.links[link]; ok && old != to {
		e.removeBacklinkLocked(from, link, old)
	}
	src.links[link] = to
	e.backlinks[to] = append(e.backlinks[to], chainEdge{source: from, link: link, target: to})
	src.version++
	e.appendEvent(src, Event{
		At:         now,
		OccurredAt: now,
		Kind:       EventLink,
		State:      src.state,
		Name:       link,
		Value:      to,
		Detail:     fmt.Sprintf("link %q -> %q", link, to),
	})
	e.emitLog("SetLink:"+link, from, now, run.rings, nil)
	return nil
}

func (e *Engine) removeBacklinkLocked(from, link, old string) {
	edges := e.backlinks[old]
	kept := edges[:0]
	for _, ed := range edges {
		if ed.source == from && ed.link == link {
			continue
		}
		kept = append(kept, ed)
	}
	e.backlinks[old] = kept
}

// PropertyOf reads a property value, settling expiry rings first.
func (e *Engine) PropertyOf(id, name string) (string, bool, error) {
	if _, err := e.Get(id); err != nil {
		return "", false, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return "", false, ferr
	}
	v, ok := in.props[name]
	return v, ok, nil
}

// LinkTarget reads the target bound to a link slot, settling first.
func (e *Engine) LinkTarget(from, link string) (string, bool, error) {
	if _, err := e.Get(from); err != nil {
		return "", false, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ferr := e.mustGetLocked(from)
	if ferr != nil {
		return "", false, ferr
	}
	v, ok := in.links[link]
	return v, ok, nil
}

func (e *Engine) emitLog(caller, id string, now time.Time, rings []SettleRecord, err *Failure) {
	if e.log == nil {
		return
	}
	call := SettleCall{Caller: caller, Instance: id, Now: now, Rings: rings}
	if err != nil {
		call.Err = err.Error()
	}
	e.log.LogSettle(call)
}
