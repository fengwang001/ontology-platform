package lifecycle

import (
	"sort"
	"sync"
	"time"
)

// ObjectType bundles the transition declarations of one object type.
type ObjectType struct {
	Name        string
	Transitions []TransitionDef
}

// chainEdge is one indexed link slot carrying a potential chained effect.
type chainEdge struct {
	source string
	link   string
	target string
}

// inst is the live state of one object instance.
type inst struct {
	id       string
	typeName string

	state   string
	entered time.Time // virtual time at which the current state was entered
	version int64     // increments on every committed change

	// highWater is the largest observation-time that ever drove settlement.
	// Fired rings are never undone; a smaller now only affects subsequent
	// due judgments.
	highWater time.Time

	props map[string]string
	links map[string]string

	history []Event
	seq     int64

	// settling marks membership in the closure currently processed under
	// the engine lock.
	settling bool
}

// SettleLogger receives one structured record per access, after settlement.
type SettleLogger interface {
	LogSettle(call SettleCall)
}

// SettleCall is the per-access settlement log record: the input (who,
// which instance, at what observation time), every ring advanced and its
// decision basis.
type SettleCall struct {
	Caller   string
	Instance string
	Now      time.Time
	Rings    []SettleRecord
	Err      string
}

// Engine is the lazy-settlement lifecycle subsystem.
type Engine struct {
	clock Clock
	log   SettleLogger

	mu    sync.Mutex
	types map[string]*ObjectType
	insts map[string]*inst

	// backlinks maps target -> incoming chain edges, maintained by
	// SetLink. Entering a chain from its middle still settles declared
	// predecessors first.
	backlinks map[string][]chainEdge

	// Simulation-only fields used by history reconstruction.
	simErrs     map[string]error
	simErrFirst error
}

// NewEngine creates an empty engine.
func NewEngine(clock Clock, logger SettleLogger) *Engine {
	if clock == nil {
		clock = time.Now
	}
	return &Engine{
		clock:     clock,
		log:       logger,
		types:     map[string]*ObjectType{},
		insts:     map[string]*inst{},
		backlinks: map[string][]chainEdge{},
	}
}

// RegisterType registers an object type declaration. At most one expiry
// transition may leave each state; forced transition names must be unique.
func (e *Engine) RegisterType(t *ObjectType) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.types[t.Name]; dup {
		return fail(ErrChainTrigger, "duplicate object type %q", t.Name)
	}
	expiryFrom := map[string]string{}
	forcedNames := map[string]bool{}
	for _, d := range t.Transitions {
		switch d.Trigger {
		case TriggerExpiry:
			if prev, ok := expiryFrom[d.From]; ok {
				return fail(ErrChainTrigger, "type %q state %q declares two expiry transitions %q and %q", t.Name, d.From, prev, d.Name)
			}
			expiryFrom[d.From] = d.Name
			if d.Duration <= 0 {
				return fail(ErrChainTrigger, "expiry transition %q must have a positive duration", d.Name)
			}
		case TriggerForced:
			if forcedNames[d.Name] {
				return fail(ErrChainTrigger, "duplicate forced transition name %q", d.Name)
			}
			forcedNames[d.Name] = true
		case TriggerAction:
			if len(d.Actions) == 0 {
				return fail(ErrChainTrigger, "action transition %q declares no action names", d.Name)
			}
		}
	}
	cp := *t
	cp.Transitions = append([]TransitionDef(nil), t.Transitions...)
	e.types[t.Name] = &cp
	return nil
}

// RegisterInstance creates an instance in initialState, entered at at.
func (e *Engine) RegisterInstance(id, typeName, initialState string, at time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.registerInstanceLocked(id, typeName, initialState, at)
}

func (e *Engine) registerInstanceLocked(id, typeName, initialState string, at time.Time) error {
	if _, dup := e.insts[id]; dup {
		return fail(ErrChainTrigger, "duplicate instance %q", id)
	}
	t, ok := e.types[typeName]
	if !ok {
		return fail(ErrChainTrigger, "unknown object type %q", typeName)
	}
	if !stateExists(t, initialState) {
		return fail(ErrChainTrigger, "type %q has no state %q", typeName, initialState)
	}
	in := &inst{
		id:        id,
		typeName:  typeName,
		state:     initialState,
		entered:   at,
		highWater: at,
		props:     map[string]string{},
		links:     map[string]string{},
	}
	in.seq = 1
	in.history = append(in.history, Event{
		Seq:      1,
		At:       at,
		TypeName: typeName,
		Kind:     EventInitial,
		State:    initialState,
	})
	e.insts[id] = in
	return nil
}

func stateExists(t *ObjectType, state string) bool {
	seen := map[string]bool{}
	for _, d := range t.Transitions {
		seen[d.From] = true
		seen[d.To] = true
	}
	return seen[state]
}

func (e *Engine) evalContext(in *inst, now time.Time) *EvalContext {
	return &EvalContext{Engine: e, Instance: in.id, Now: now}
}

func (e *Engine) typeOf(in *inst) *ObjectType { return e.types[in.typeName] }

// expiryTransition returns the unique expiry transition leaving state.
func expiryTransition(t *ObjectType, state string) (TransitionDef, bool) {
	for _, d := range t.Transitions {
		if d.Trigger == TriggerExpiry && d.From == state {
			return d, true
		}
	}
	return TransitionDef{}, false
}

func forcedTransition(t *ObjectType, name string) (TransitionDef, bool) {
	for _, d := range t.Transitions {
		if d.Trigger == TriggerForced && d.Name == name {
			return d, true
		}
	}
	return TransitionDef{}, false
}

func actionTransition(t *ObjectType, state, action string) (TransitionDef, bool) {
	for _, d := range t.Transitions {
		if d.Trigger != TriggerAction || d.From != state {
			continue
		}
		for _, a := range d.Actions {
			if a == action {
				return d, true
			}
		}
	}
	return TransitionDef{}, false
}

func (e *Engine) appendEvent(in *inst, ev Event) {
	in.seq++
	ev.Seq = in.seq
	in.history = append(in.history, ev)
}

func (e *Engine) mustGetLocked(id string) (*inst, *Failure) {
	in, ok := e.insts[id]
	if !ok {
		return nil, fail(ErrChainTrigger, "unknown instance %q", id)
	}
	return in, nil
}

func (e *Engine) snapshotLocked(in *inst, now time.Time) Snapshot {
	return Snapshot{
		Instance:  in.id,
		State:     in.state,
		EnteredAt: in.entered,
		Version:   in.version,
		Now:       now,
	}
}

// sortedInstanceIDsLocked is used by the naive scanner and diagnostics.
func (e *Engine) sortedInstanceIDsLocked() []string {
	ids := make([]string, 0, len(e.insts))
	for id := range e.insts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
