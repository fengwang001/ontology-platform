package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ChangeEvent is one logged, fully-committed change.
type ChangeEvent struct {
	Seq       int
	Op        string
	View      string
	Input     string
	Touched   []string
	Rationale string
	Before    map[string]Aggregate
	After     map[string]Aggregate
}

// FailHook is invoked inside a processing unit after validation but before any
// mutation. Returning an error aborts the unit with ClassTxnFailed and leaves
// every aggregate unchanged. It is the extension point that proves atomic
// rollback of a property write / membership change.
type FailHook func(view, op string) error

// Engine incrementally maintains aggregation views over a Store.
type Engine struct {
	mu    sync.Mutex
	store *Store
	views map[string]*ViewDef
	state map[string]*viewState

	seq      int
	events   []ChangeEvent
	failHook FailHook
}

type viewState struct {
	member map[string]*membership
	group  map[string]*groupAgg
}

// NewEngine creates an engine backed by store.
func NewEngine(store *Store) *Engine {
	return &Engine{
		store: store,
		views: map[string]*ViewDef{},
		state: map[string]*viewState{},
	}
}

// SetFailHook installs a hook that can force a processing unit to fail before
// commit (test support).
func (e *Engine) SetFailHook(h FailHook) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failHook = h
}

// RegisterView declares a new aggregation view.
func (e *Engine) RegisterView(v ViewDef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case v.Name == "":
		return classified(ClassInvalid, "registerView", "view name required")
	case v.GroupType == "" || v.AggType == "" || v.LinkType == "" || v.ValueProperty == "":
		return classified(ClassInvalid, "registerView", "group/agg type, link type and value property are required")
	case !v.Contribution.valid():
		return classified(ClassInvalid, "registerView", "an explicit contribution policy is required")
	}
	if _, ok := e.views[v.Name]; ok {
		return classified(ClassInvalid, "registerView", "view already exists: "+v.Name)
	}
	cp := v
	e.views[v.Name] = &cp
	e.state[v.Name] = &viewState{
		member: map[string]*membership{},
		group:  map[string]*groupAgg{},
	}
	return nil
}

func (e *Engine) viewLocked(name string) (*ViewDef, *viewState, error) {
	v := e.views[name]
	if v == nil {
		return nil, nil, classified(ClassInvalid, "view", "unknown view: "+name)
	}
	return v, e.state[name], nil
}

func (vs *viewState) ensureGroup(groupID string) *groupAgg {
	g := vs.group[groupID]
	if g == nil {
		g = &groupAgg{}
		vs.group[groupID] = g
	}
	return g
}

// valueOf reads the aggregated property. A property that was never written (or
// was cleared) is absent, not zero.
func (e *Engine) valueOf(v *ViewDef, aggID string) Optional {
	opt, _ := e.store.GetProperty(v.AggType, aggID, v.ValueProperty)
	return opt
}

func participation(opt Optional) (float64, int) {
	if !opt.Present {
		return 0, 0
	}
	return opt.Value, 1
}

func (vs *viewState) snapshot(groups []string) map[string]Aggregate {
	out := make(map[string]Aggregate, len(groups))
	for _, g := range groups {
		a := vs.group[g]
		if a == nil {
			out[g] = Aggregate{}
		} else {
			out[g] = Aggregate{Sum: a.sum, Count: a.count}
		}
	}
	return out
}

func (e *Engine) commit(v *ViewDef, vs *viewState, op, input, rationale string, touched []string, mutate func()) OpResult {
	sort.Strings(touched)
	touched = dedup(touched)
	before := vs.snapshot(touched)
	if e.failHook != nil {
		if err := e.failHook(v.Name, op); err != nil {
			_ = err
			panic(abortErr{op: op})
		}
	}
	mutate()
	after := vs.snapshot(touched)
	e.seq++
	ev := ChangeEvent{
		Seq: e.seq, Op: op, View: v.Name, Input: input,
		Touched: append([]string(nil), touched...), Rationale: rationale,
		Before: before, After: after,
	}
	e.events = append(e.events, ev)
	return OpResult{TouchedGroups: ev.Touched, TouchedCount: len(ev.Touched), Seq: e.seq, Events: []ChangeEvent{ev}}
}

type abortErr struct{ op string }

// runUnit executes op under the engine lock with fixed-priority error
// classification and atomic rollback: anything panicked as abortErr (the
// failHook fired before mutation) or txnPanic (a step failed mid-mutation) is
// restored from the pre-unit snapshot and reported as ClassTxnFailed.
func (e *Engine) runUnit(op string, body func() (OpResult, error)) (res OpResult, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap := e.snapshotState()
	storeSnap := e.store.snapshot()
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case abortErr, txnPanic:
				e.store.restore(storeSnap)
				e.restoreState(snap)
				res = OpResult{}
				err = classified(ClassTxnFailed, op, "processing unit failed; aggregate updates rolled back")
			default:
				e.store.restore(storeSnap)
				e.restoreState(snap)
				panic(r)
			}
		}
	}()
	res, err = body()
	if err != nil {
		e.store.restore(storeSnap)
		e.restoreState(snap)
		res = OpResult{}
	}
	return res, err
}

type txnPanic struct{}

type fullSnapshot struct {
	state  map[string]map[string]groupAgg
	member map[string]map[string]membershipSnap
}

type membershipSnap struct {
	groups  []string
	contrib map[string]float64
	present map[string]bool
}

func (e *Engine) snapshotState() fullSnapshot {
	gs := make(map[string]map[string]groupAgg, len(e.state))
	ms := make(map[string]map[string]membershipSnap, len(e.state))
	for name, vs := range e.state {
		gm := map[string]groupAgg{}
		for id, g := range vs.group {
			gm[id] = *g
		}
		gs[name] = gm
		mm := map[string]membershipSnap{}
		for id, m := range vs.member {
			cp := make(map[string]float64, len(m.contrib))
			for k, val := range m.contrib {
				cp[k] = val
			}
			pp := make(map[string]bool, len(m.present))
			for k, val := range m.present {
				pp[k] = val
			}
			mm[id] = membershipSnap{groups: append([]string(nil), m.groups...), contrib: cp, present: pp}
		}
		ms[name] = mm
	}
	return fullSnapshot{state: gs, member: ms}
}

func (e *Engine) restoreState(s fullSnapshot) {
	for name, vs := range e.state {
		vs.group = map[string]*groupAgg{}
		for id, g := range s.state[name] {
			gv := g
			vs.group[id] = &gv
		}
		vs.member = map[string]*membership{}
		for id, m := range s.member[name] {
			cp := make(map[string]float64, len(m.contrib))
			for k, v := range m.contrib {
				cp[k] = v
			}
			vs.member[id] = &membership{groups: append([]string(nil), m.groups...), contrib: cp, present: m.present}
		}
	}
}

// requireGroupAndMember applies the fixed priority: missing group beats
// non-participating aggregated type.
func (e *Engine) requireGroupAndMember(v *ViewDef, op, groupID, aggID string) error {
	if groupID != "" && !e.store.ObjectExists(v.GroupType, groupID) {
		return classified(ClassGroupMissing, op, fmt.Sprintf("group instance %q of type %q does not exist", groupID, v.GroupType))
	}
	if !e.store.ObjectExists(v.AggType, aggID) {
		return classified(ClassTypeNotParticipating, op, fmt.Sprintf("instance %q is not of aggregated type %q declared by view %q", aggID, v.AggType, v.Name))
	}
	return nil
}

func dedup(a []string) []string {
	if len(a) < 2 {
		return a
	}
	out := a[:1]
	for _, s := range a[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// OpResult reports a committed processing unit.
type OpResult struct {
	TouchedGroups []string
	TouchedCount  int
	Seq           int
	Events        []ChangeEvent
}
