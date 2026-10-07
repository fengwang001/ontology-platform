package lifecycle

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// Snapshot is the materialized state of an instance after lazy settlement.
type Snapshot struct {
	Instance   InstanceID
	Type       ObjectTypeID
	State      StateID
	EnteredAt  time.Time
	Generation int64
}

type Metrics struct {
	NewlySettledSteps int64
	TimeGuardsChecked int64
	CascadesResolved  int64
	HistoryScans      int64
}

type SettlementInput struct {
	At      time.Time
	Trigger string
	Root    InstanceID
	Order   []InstanceID
}

type SettlementStep struct {
	Instance   InstanceID
	Kind       EntryKind
	Transition TransitionID
	From       StateID
	To         StateID
	DueAt      time.Time
	LogicalAt  time.Time
	Fired      bool
	Reason     string
}

// Logger observes every lazy settlement: its input, the steps advanced, the
// decision basis, and any errors.
type Logger interface {
	Log(input SettlementInput, steps []SettlementStep, errs ErrorList)
}

type instance struct {
	id     InstanceID
	typeID ObjectTypeID
	state  StateID
	// enteredAt is the logical entry time of state: the single clock source
	// for every due-time decision. It advances only when a transition is
	// materialized and never moves backwards.
	enteredAt  time.Time
	generation int64
	props      map[string]any
	links      map[InstanceID]bool
	history    *History
}

type Engine struct {
	clock Clock
	mu    sync.Mutex

	types     map[ObjectTypeID]*ObjectType
	instances map[InstanceID]*instance

	logger  Logger
	metrics Metrics
}

func NewEngine(c Clock) *Engine {
	if c == nil {
		c = wallClock{}
	}
	return &Engine{
		clock:     c,
		types:     make(map[ObjectTypeID]*ObjectType),
		instances: make(map[InstanceID]*instance),
	}
}

func (e *Engine) SetLogger(l Logger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger = l
}

func (e *Engine) RegisterType(t *ObjectType) error {
	if t == nil {
		return fmt.Errorf("RegisterType: nil type")
	}
	if err := t.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.types[t.ID]; exists {
		return fmt.Errorf("object type %s already registered", t.ID)
	}
	e.types[t.ID] = t
	return nil
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

var _ = os.Stdout

func (e *Engine) Spawn(typeID ObjectTypeID, id InstanceID, initial StateID, props map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeID]
	if !ok {
		return fmt.Errorf("unknown object type %s", typeID)
	}
	if _, exists := e.instances[id]; exists {
		return fmt.Errorf("instance %s already exists", id)
	}
	if initial == "" {
		initial = t.Initial
	}
	if !stateDeclared(t, initial) {
		return fmt.Errorf("initial state %s not declared on type %s", initial, typeID)
	}
	now := e.clock.Now()
	instProps := make(map[string]any, len(props))
	for k, v := range props {
		instProps[k] = v
	}
	e.instances[id] = &instance{
		id:        id,
		typeID:    typeID,
		state:     initial,
		enteredAt: now,
		props:     instProps,
		links:     make(map[InstanceID]bool),
		history:   NewHistory(initial, now),
	}
	return nil
}

func stateDeclared(t *ObjectType, s StateID) bool {
	for _, declared := range t.States {
		if declared == s {
			return true
		}
	}
	return false
}

func (e *Engine) AddLink(from, to InstanceID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if inst, ok := e.instances[from]; ok {
		inst.links[to] = true
	}
}

func (e *Engine) RemoveLink(from, to InstanceID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if inst, ok := e.instances[from]; ok {
		delete(inst.links, to)
	}
}

// Read materializes all due settlement before returning the instance state.
func (e *Engine) Read(id InstanceID) (Snapshot, ErrorList) {
	e.mu.Lock()
	defer e.mu.Unlock()
	errs := e.settle(id, "read")
	inst, ok := e.instances[id]
	if !ok {
		return Snapshot{}, append(errs, &Error{Kind: KindActionDenied, Instance: id, Op: "read", Detail: "no such instance"})
	}
	return e.snapshot(inst), errs
}

// Action first materializes all due settlement, then evaluates the explicit
// action strictly against the settled state. A rejected action mutates
// nothing; settlement already materialized during the call stays in effect.
func (e *Engine) Action(id InstanceID, transition TransitionID) (Snapshot, ErrorList) {
	e.mu.Lock()
	defer e.mu.Unlock()
	errs := e.settle(id, "action:"+string(transition))
	inst, ok := e.instances[id]
	if !ok {
		return Snapshot{}, append(errs, &Error{Kind: KindActionDenied, Instance: id, Transition: transition, Op: "action", Detail: "no such instance"})
	}
	if primary := errs.Primary(); primary != nil && primary.Kind == KindClockRegression {
		// While the clock reports a time behind a materialized entry time,
		// no explicit action may be admitted against settled state.
		return e.snapshot(inst), errs
	}
	t := e.types[inst.typeID]
	tr := t.FindTransition(transition)
	if tr == nil || tr.Kind != KindActionTransition {
		errs = append(errs, &Error{Kind: KindActionDenied, Instance: id, Transition: transition, Op: "action", Detail: "unknown action transition"})
		return e.snapshot(inst), errs
	}
	if tr.From != inst.state {
		errs = append(errs, &Error{Kind: KindActionDenied, Instance: id, Transition: transition, Op: "action", Detail: fmt.Sprintf("state %s does not match source %s", inst.state, tr.From)})
		return e.snapshot(inst), errs
	}
	if tr.Guard != nil {
		e.metrics.TimeGuardsChecked++
		if !tr.Guard(e.guardContext(inst)) {
			errs = append(errs, &Error{Kind: KindActionDenied, Instance: id, Transition: transition, Op: "action", Detail: "precondition not satisfied after settlement"})
			return e.snapshot(inst), errs
		}
	}
	now := e.clock.Now()
	e.applyTransition(inst, tr, EntryAction, now, now, "explicit action")
	e.applyEffect(inst, tr.Effect)
	return e.snapshot(inst), errs
}

// StateAt re-derives the state at an arbitrary time purely from the immutable
// history. It never consults the current materialized state and mutates
// nothing, so its answer is independent of how far lazy settlement advanced.
func (e *Engine) StateAt(id InstanceID, at time.Time) (StateID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst, ok := e.instances[id]
	if !ok {
		return "", fmt.Errorf("no such instance %s", id)
	}
	e.metrics.HistoryScans++
	return inst.history.Replay(at), nil
}

func (e *Engine) History(id InstanceID) ([]Entry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst, ok := e.instances[id]
	if !ok {
		return nil, fmt.Errorf("no such instance %s", id)
	}
	return inst.history.Entries(), nil
}

func (e *Engine) Metrics() Metrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.metrics
}

func (e *Engine) ResetMetrics() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.metrics = Metrics{}
}

func (e *Engine) snapshot(inst *instance) Snapshot {
	return Snapshot{
		Instance:   inst.id,
		Type:       inst.typeID,
		State:      inst.state,
		EnteredAt:  inst.enteredAt,
		Generation: inst.generation,
	}
}

func (e *Engine) guardContext(inst *instance) GuardContext {
	return GuardContext{store: e, Self: inst.id}
}

func (e *Engine) propOf(id InstanceID, key string) (any, bool) {
	inst, ok := e.instances[id]
	if !ok {
		return nil, false
	}
	v, ok := inst.props[key]
	return v, ok
}

func (e *Engine) hasLink(from, to InstanceID) bool {
	inst, ok := e.instances[from]
	return ok && inst.links[to]
}

// settle materializes the whole cascade closure needed to answer one access
// to root. Settlement order always respects the declared chain: a strict
// upstream is settled before any downstream instance, regardless of which
// instance the call entered through.
func (e *Engine) settle(root InstanceID, trigger string) ErrorList {
	now := e.clock.Now()
	var errs ErrorList
	var steps []SettlementStep

	blocked := make(map[InstanceID]bool)
	var order []InstanceID
	for progress := true; progress; {
		progress = false
		// Re-expand the closure every round: a cascade just fired may expose
		// a new pending time transition on the target, whose own cascade then
		// pulls further upstream instances into the ordered closure.
		order = e.settleOrder(root)
		for _, id := range order {
			if blocked[id] {
				continue
			}
			inst, ok := e.instances[id]
			if !ok {
				continue
			}
			moved, instErrs, instSteps := e.settleOne(inst, now)
			errs = append(errs, instErrs...)
			steps = append(steps, instSteps...)
			if len(instErrs) > 0 {
				// This instance cannot advance further during this call.
				blocked[id] = true
				continue
			}
			if moved {
				progress = true
			} else {
				blocked[id] = true
			}
		}
	}

	e.logSettlement(SettlementInput{At: now, Trigger: trigger, Root: root, Order: order}, steps, errs)
	return errs
}

// settleOrder returns root's cascade closure in declared-chain order: every
// instance whose pending time transition currently cascades (transitively)
// to root appears before root. Edges are read off the materialized state.
// Ties between unrelated instances break deterministically by id.
func (e *Engine) settleOrder(root InstanceID) []InstanceID {
	upstream := make(map[InstanceID]map[InstanceID]bool)
	ids := make([]InstanceID, 0, len(e.instances))
	for id := range e.instances {
		ids = append(ids, id)
	}
	sortInstanceIDs(ids)
	for _, id := range ids {
		inst := e.instances[id]
		tr := e.types[inst.typeID].TimeTransitionFrom(inst.state)
		if tr == nil || tr.Cascade == nil {
			continue
		}
		e.metrics.CascadesResolved++
		target, ok := tr.Cascade.Target(e.guardContext(inst))
		if !ok {
			continue
		}
		if _, exists := e.instances[target]; !exists {
			continue
		}
		if upstream[target] == nil {
			upstream[target] = make(map[InstanceID]bool)
		}
		upstream[target][id] = true
	}
	visited := make(map[InstanceID]bool)
	var order []InstanceID
	var visit func(id InstanceID)
	visit = func(id InstanceID) {
		if visited[id] {
			return
		}
		visited[id] = true
		var upIDs []InstanceID
		for up := range upstream[id] {
			upIDs = append(upIDs, up)
		}
		sortInstanceIDs(upIDs)
		for _, up := range upIDs {
			visit(up)
		}
		order = append(order, id)
	}
	visit(root)
	return order
}

func sortInstanceIDs(ids []InstanceID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

// settleOne drains the currently due time-transition chain of a single
// instance and every cascade fired by its steps. It reports whether at least
// one step was materialized.
func (e *Engine) settleOne(inst *instance, now time.Time) (bool, ErrorList, []SettlementStep) {
	t := e.types[inst.typeID]
	var errs ErrorList
	var steps []SettlementStep
	moved := false
	for {
		// A materialized entry time is an irreversible fact; if the clock
		// regressed below it, further due-time math is anomalous and stops.
		if now.Before(inst.enteredAt) {
			errs = append(errs, &Error{
				Kind:     KindClockRegression,
				Instance: inst.id,
				Op:       "settle",
				Detail: fmt.Sprintf("current time %s precedes materialized state entry time %s",
					now.Format(time.RFC3339Nano), inst.enteredAt.Format(time.RFC3339Nano)),
			})
			return moved, errs, steps
		}
		tr := t.TimeTransitionFrom(inst.state)
		if tr == nil {
			return moved, errs, steps
		}
		due := inst.enteredAt.Add(tr.Duration)
		if now.Before(due) {
			return moved, errs, steps
		}
		if tr.Guard != nil {
			e.metrics.TimeGuardsChecked++
			if !tr.Guard(e.guardContext(inst)) {
				// Halt in place without touching enteredAt: the clock source
				// still points at this state, so a later access re-evaluates
				// the guard instead of consuming the deadline.
				steps = append(steps, SettlementStep{
					Instance:   inst.id,
					Kind:       EntryTime,
					Transition: tr.ID,
					From:       inst.state,
					To:         tr.To,
					DueAt:      due,
					LogicalAt:  due,
					Fired:      false,
					Reason:     "due but non-temporal precondition failed; state entry time unchanged",
				})
				errs = append(errs, &Error{Kind: KindGuardBlocked, Instance: inst.id, Transition: tr.ID, Op: "settle", Detail: "time transition due but guard failed"})
				return moved, errs, steps
			}
		}
		e.applyTransition(inst, tr, EntryTime, due, now, "due time transition")
		e.applyEffect(inst, tr.Effect)
		moved = true
		e.metrics.NewlySettledSteps++
		steps = append(steps, SettlementStep{
			Instance:   inst.id,
			Kind:       EntryTime,
			Transition: tr.ID,
			From:       tr.From,
			To:         tr.To,
			DueAt:      due,
			LogicalAt:  due,
			Fired:      true,
			Reason:     fmt.Sprintf("entered %s at %s + %s elapsed by %s", tr.From, inst.history.Entries()[len(inst.history.Entries())-2].At.Format(time.RFC3339Nano), tr.Duration, now.Format(time.RFC3339Nano)),
		})
		if tr.Cascade != nil {
			casErrs, casSteps := e.fireCascade(inst, tr, due, now)
			errs = append(errs, casErrs...)
			steps = append(steps, casSteps...)
			if len(casErrs) > 0 {
				return moved, errs, steps
			}
		}
	}
}

// fireCascade forces the declared transition on the target instance. The
// forced transition is stamped with the firing transition's logical time, so
// the chain preserves its declared causal/temporal ordering.
func (e *Engine) fireCascade(src *instance, tr *Transition, logicalAt, settledAt time.Time) (ErrorList, []SettlementStep) {
	targetID, ok := tr.Cascade.Target(e.guardContext(src))
	if !ok {
		return ErrorList{&Error{Kind: KindCascadeFailed, Instance: src.id, Transition: tr.ID, Op: "cascade", Detail: "cascade target could not be resolved"}}, nil
	}
	target, exists := e.instances[targetID]
	if !exists {
		return ErrorList{&Error{Kind: KindCascadeFailed, Instance: targetID, Transition: tr.Cascade.Transition, Op: "cascade", Detail: "target instance does not exist"}}, nil
	}
	targetType := e.types[target.typeID]
	forced := targetType.FindTransition(tr.Cascade.Transition)
	if forced == nil || forced.From != target.state {
		return ErrorList{&Error{Kind: KindCascadeFailed, Instance: targetID, Transition: tr.Cascade.Transition, Op: "cascade", Detail: fmt.Sprintf("forced transition not enabled in target state %s", target.state)}}, nil
	}
	// A cascade is mandatory and unconditional by declaration: guards on the
	// forced transition are intentionally not consulted.
	e.applyTransition(target, forced, EntryCascade, logicalAt, settledAt, "forced by cascade of "+string(tr.ID))
	e.applyEffect(target, forced.Effect)
	e.metrics.NewlySettledSteps++
	steps := []SettlementStep{{
		Instance:   targetID,
		Kind:       EntryCascade,
		Transition: forced.ID,
		From:       forced.From,
		To:         forced.To,
		LogicalAt:  logicalAt,
		Fired:      true,
		Reason:     fmt.Sprintf("cascade from %s via %s", src.id, tr.ID),
	}}
	// The forced transition may itself declare a cascade onward: process it
	// immediately and recursively so the whole declared chain advances in
	// order within the originating settlement.
	if forced.Cascade != nil {
		casErrs, more := e.fireCascade(target, forced, logicalAt, settledAt)
		if len(casErrs) > 0 {
			return casErrs, steps
		}
		steps = append(steps, more...)
	}
	return nil, steps
}

// applyTransition is the single mutation point: it moves state, advances the
// logical entry time (never backwards), bumps the generation, and appends an
// immutable history entry.
func (e *Engine) applyTransition(inst *instance, tr *Transition, kind EntryKind, logicalAt, settledAt time.Time, reason string) {
	inst.generation++
	entry := Entry{
		Kind:       kind,
		Transition: tr.ID,
		From:       inst.state,
		To:         tr.To,
		At:         logicalAt,
		SettledAt:  settledAt,
		Generation: inst.generation,
		Reason:     reason,
	}
	if kind == EntryTime {
		entry.DueAt = logicalAt
	}
	inst.history.Append(entry)
	inst.state = tr.To
	// enteredAt advances forward only; equal is a no-op and earlier values
	// can never undo a materialized fact.
	if logicalAt.After(inst.enteredAt) {
		inst.enteredAt = logicalAt
	}
}

func (e *Engine) applyEffect(inst *instance, effect *Effect) {
	if effect == nil {
		return
	}
	for k, v := range effect.SetProps {
		inst.props[k] = v
	}
	for _, target := range effect.AddLinks {
		inst.links[target] = true
	}
	for _, target := range effect.RemoveLinks {
		delete(inst.links, target)
	}
}

func (e *Engine) logSettlement(input SettlementInput, steps []SettlementStep, errs ErrorList) {
	if e.logger != nil {
		e.logger.Log(input, steps, errs)
	}
}

// StdLogger writes human-readable settlement logs to w (defaults to stderr).
type StdLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewStdLogger(w io.Writer) *StdLogger {
	if w == nil {
		w = os.Stderr
	}
	return &StdLogger{w: w}
}

func (l *StdLogger) Log(input SettlementInput, steps []SettlementStep, errs ErrorList) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "[settle] at=%s trigger=%q root=%s order=%v steps=%d\n",
		input.At.Format(time.RFC3339Nano), input.Trigger, input.Root, input.Order, len(steps))
	for _, s := range steps {
		fmt.Fprintf(l.w, "    - instance=%s kind=%s transition=%s %s->%s due=%s logicalAt=%s fired=%t basis=%q\n",
			s.Instance, entryKindName(s.Kind), s.Transition, s.From, s.To,
			formatTime(s.DueAt), formatTime(s.LogicalAt), s.Fired, s.Reason)
	}
	for _, err := range errs {
		if err != nil {
			fmt.Fprintf(l.w, "    ! %s (priority %d)\n", err.Error(), err.Kind.Priority())
		}
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339Nano)
}

func entryKindName(k EntryKind) string {
	switch k {
	case EntryInit:
		return "init"
	case EntryTime:
		return "time"
	case EntryAction:
		return "action"
	case EntryCascade:
		return "cascade"
	default:
		return "?"
	}
}
