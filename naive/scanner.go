// Package naive contains an independently implemented reference model: a
// periodic background scanner that eagerly performs due expiry transitions.
// It deliberately reuses none of the lazy engine's settlement code, so the
// differential tests compare two genuinely different implementations.
package naive

import (
	"fmt"
	"sort"
	"time"
)

// Guard is a property/link precondition over the simulated world.
type Guard func(w *World, id string) bool

// Transition is one declaration in the naive model.
type Transition struct {
	Name     string
	From     string
	To       string
	Duration time.Duration // expiry only
	Action   string        // non-empty => explicit action
	Guard    Guard

	// Chain declares cross-instance effects as (link, targetFrom, name).
	Chain []ChainEffect
}

// ChainEffect forces ForcedName on the instance reached via Link.
type ChainEffect struct {
	Link       string
	TargetFrom string
	ForcedName string
	Guard      Guard
}

// Type is an object type declaration.
type Type struct {
	Name        string
	Transitions []Transition
}

// Instance is a live naive-model instance.
type Instance struct {
	ID      string
	Type    string
	State   string
	Entered time.Time
	Props   map[string]string
	Links   map[string]string
}

// World is the naive eagerly-scanned simulation.
type World struct {
	types     map[string]*Type
	instances map[string]*Instance
	now       time.Time

	// Error returned by the last Tick/Act/Get, classified 1..4 with the
	// same fixed priority as the lazy engine.
	LastError string
	LastCat   int

	// RingsFiredOnLastTick counts firings on the most recent scan.
	RingsFiredOnLastTick int

	// backlinks mirrors the lazy engine's chain predecessor index.
	backlinks map[string][]backEdge
}

type backEdge struct{ source, link string }

// NewWorld creates an empty naive world.
func NewWorld() *World {
	return &World{
		types:     map[string]*Type{},
		instances: map[string]*Instance{},
		backlinks: map[string][]backEdge{},
	}
}

// RegisterType adds a type declaration.
func (w *World) RegisterType(t *Type) {
	cp := &Type{Name: t.Name, Transitions: append([]Transition(nil), t.Transitions...)}
	w.types[t.Name] = cp
}

// RegisterInstance creates an instance.
func (w *World) RegisterInstance(id, typeName, state string, at time.Time) {
	w.instances[id] = &Instance{
		ID:      id,
		Type:    typeName,
		State:   state,
		Entered: at,
		Props:   map[string]string{},
		Links:   map[string]string{},
	}
}

// SetProperty mutates a property.
func (w *World) SetProperty(id, name, value string) {
	w.instances[id].Props[name] = value
}

// SetLink binds a link slot.
func (w *World) SetLink(from, link, to string) {
	if old, ok := w.instances[from].Links[link]; ok {
		kept := w.backlinks[old][:0]
		for _, e := range w.backlinks[old] {
			if e.source != from || e.link != link {
				kept = append(kept, e)
			}
		}
		w.backlinks[old] = kept
	}
	w.instances[from].Links[link] = to
	w.backlinks[to] = append(w.backlinks[to], backEdge{source: from, link: link})
}

// tickOne scans exactly the chain closure reached from id (following both
// outgoing links and backlinks), matching the lazy engine's access scope.
func (w *World) tickOne(id string, now time.Time) error {
	if !w.now.IsZero() && now.Before(w.now) {
		w.record(4, fmt.Sprintf("clock regression %s -> %s", w.now, now))
	}
	w.now = now
	w.RingsFiredOnLastTick = 0

	visited := map[string]bool{id: true}
	var walk func(string)
	walk = func(x string) {
		in := w.instances[x]
		t := w.types[in.Type]
		for _, d := range t.Transitions {
			for _, ch := range d.Chain {
				if target, ok := in.Links[ch.Link]; ok {
					if !visited[target] {
						visited[target] = true
						walk(target)
					}
				}
			}
		}
		for _, e := range w.backlinks[x] {
			if !visited[e.source] {
				visited[e.source] = true
				walk(e.source)
			}
		}
	}
	walk(id)

	var set []string
	for x := range visited {
		set = append(set, x)
	}
	// Order the closure by declared chain direction (sources first) so a
	// ring and its forced effect land in the same scan pass, matching the
	// lazy engine's declared chain order.
	set = w.topoOrder(set)

	for {
		progress := false
		for _, x := range set {
			in := w.instances[x]
			t := w.types[in.Type]
			var due *Transition
			for i := range t.Transitions {
				d := &t.Transitions[i]
				if d.Duration > 0 && d.From == in.State &&
					!in.Entered.Add(d.Duration).After(now) {
					due = d
					break
				}
			}
			if due == nil {
				continue
			}
			if due.Guard != nil && !due.Guard(w, x) {
				w.record(1, fmt.Sprintf("expiry guard failed for %s", due.Name))
				return fmt.Errorf("naive: expiry guard failed for %s", due.Name)
			}
			dueAt := in.Entered.Add(due.Duration)
			in.State = due.To
			in.Entered = dueAt
			w.RingsFiredOnLastTick++
			progress = true
			if err := w.applyChain(in, *due, dueAt); err != nil {
				return err
			}
		}
		if !progress {
			return nil
		}
	}
}

// State reports a state after scanning to now. The scanner is given the
// same access trigger as the lazy engine: only the accessed instance (and
// its declared chain) is evaluated, so a never-visited instance is not
// settled eagerly here either.
func (w *World) State(id string, now time.Time) (string, error) {
	if err := w.tickOne(id, now); err != nil {
		return w.instances[id].State, err
	}
	return w.instances[id].State, nil
}

// Act performs an explicit action after scanning to now.
func (w *World) Act(id, action string, now time.Time) (string, error) {
	if err := w.tickOne(id, now); err != nil {
		return w.instances[id].State, err
	}
	in := w.instances[id]
	t := w.types[in.Type]
	for _, d := range t.Transitions {
		if d.Action != action || d.From != in.State {
			continue
		}
		if d.Guard != nil && !d.Guard(w, id) {
			w.record(3, "action guard failed")
			return in.State, fmt.Errorf("naive: action %s guard failed", action)
		}
		in.State = d.To
		in.Entered = now
		if err := w.applyChain(in, d, now); err != nil {
			return in.State, err
		}
		return in.State, nil
	}
	w.record(3, "no such action")
	return in.State, fmt.Errorf("naive: action %s not enabled in state %s", action, in.State)
}

// Tick performs scan passes until a full pass fires nothing (fixpoint) —
// this is the "periodic scan at every tick" eager reference.
func (w *World) Tick(now time.Time) error {
	if !w.now.IsZero() && now.Before(w.now) {
		// Clock regression: note the anomaly (category 4), but never undo
		// already-committed rings.
		w.record(4, fmt.Sprintf("clock regression %s -> %s", w.now, now))
	}
	w.now = now
	w.RingsFiredOnLastTick = 0

	for {
		progress := false
		for _, id := range w.sortedIDs() {
			in := w.instances[id]
			t := w.types[in.Type]
			var due *Transition
			for i := range t.Transitions {
				d := &t.Transitions[i]
				if d.Duration > 0 && d.From == in.State &&
					!in.Entered.Add(d.Duration).After(now) {
					due = d
					break
				}
			}
			if due == nil {
				continue
			}
			if due.Guard != nil && !due.Guard(w, id) {
				w.record(1, fmt.Sprintf("expiry guard failed for %s", due.Name))
				return fmt.Errorf("naive: expiry guard failed for %s", due.Name)
			}
			dueAt := in.Entered.Add(due.Duration)
			in.State = due.To
			in.Entered = dueAt
			w.RingsFiredOnLastTick++
			progress = true
			if err := w.applyChain(in, *due, dueAt); err != nil {
				return err
			}
		}
		if !progress {
			return nil
		}
	}
}

// applyChain applies chained forced effects in declared order. The target
// is scanned again afterwards by the outer fixpoint, which reproduces the
// declared chain order.
func (w *World) applyChain(src *Instance, d Transition, at time.Time) error {
	for _, ch := range d.Chain {
		targetID, linked := src.Links[ch.Link]
		if !linked {
			w.record(2, "unbound chain link")
			return fmt.Errorf("naive: unbound chain link %s", ch.Link)
		}
		target := w.instances[targetID]
		if target.State != ch.TargetFrom {
			w.record(2, "target in unexpected state")
			return fmt.Errorf("naive: chain target %s in state %s", targetID, target.State)
		}
		if ch.Guard != nil && !ch.Guard(w, targetID) {
			w.record(2, "chain guard failed")
			return fmt.Errorf("naive: chain guard failed on %s", targetID)
		}
		t := w.types[target.Type]
		var def *Transition
		for i := range t.Transitions {
			if t.Transitions[i].Name == ch.ForcedName {
				def = &t.Transitions[i]
			}
		}
		if def == nil {
			w.record(2, "missing forced transition")
			return fmt.Errorf("naive: no forced transition %s", ch.ForcedName)
		}
		target.State = def.To
		target.Entered = at
		if err := w.applyChain(target, *def, at); err != nil {
			return err
		}
	}
	return nil
}

func (w *World) record(cat int, msg string) {
	w.LastCat = cat
	w.LastError = msg
}

func (w *World) sortedIDs() []string {
	ids := make([]string, 0, len(w.instances))
	for id := range w.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// topoOrder returns the given instance ids ordered along declared chain
// edges (source before target). Ties fall back to lexical order.
func (w *World) topoOrder(ids []string) []string {
	inSet := map[string]bool{}
	for _, id := range ids {
		inSet[id] = true
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, id := range ids {
		indeg[id] += 0
		in := w.instances[id]
		t := w.types[in.Type]
		for _, d := range t.Transitions {
			for _, ch := range d.Chain {
				if target, ok := in.Links[ch.Link]; ok && inSet[target] {
					adj[id] = append(adj[id], target)
					indeg[target]++
				}
			}
		}
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	var ready []string
	for _, id := range sorted {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	var out []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		targets := append([]string(nil), adj[id]...)
		sort.Strings(targets)
		for _, next := range targets {
			indeg[next]--
			if indeg[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	if len(out) != len(ids) {
		return ids // cycle: lexical fallback
	}
	return out
}

// Property reads a property value in the naive world.
func (w *World) Property(id, name string) string { return w.instances[id].Props[name] }

// Link reads a link target in the naive world.
func (w *World) Link(id, link string) string { return w.instances[id].Links[link] }
