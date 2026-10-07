package lifecycle

import (
	"fmt"
	"sync"
	"time"
)

// scanInstance is the Scanner's own representation. It intentionally does
// not reuse Engine.instance or any Engine settlement routine: the Scanner is
// an independent re-implementation used to cross-check the lazy model.
type scanInstance struct {
	id        InstanceID
	typeID    ObjectTypeID
	state     StateID
	enteredAt time.Time
	props     map[string]any
	links     map[InstanceID]bool
	history   *History
}

// Scanner is a naive periodic-scan model. Every tick it repeatedly scans
// every instance, advancing any due time transition (and its cascade), until
// a full scan produces no change. Ticks are monotonic; the clock-regression
// rule is therefore exercised solely against the lazy Engine.
type Scanner struct {
	mu        sync.Mutex
	types     map[ObjectTypeID]*ObjectType
	instances map[InstanceID]*scanInstance
	interval  time.Duration
	now       time.Time
	logger    Logger
}

func NewScanner(interval time.Duration, start time.Time) *Scanner {
	return &Scanner{
		types:     make(map[ObjectTypeID]*ObjectType),
		instances: make(map[InstanceID]*scanInstance),
		interval:  interval,
		now:       start,
	}
}

func (s *Scanner) SetLogger(l Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = l
}

func (s *Scanner) RegisterType(t *ObjectType) error {
	if t == nil {
		return fmt.Errorf("RegisterType: nil type")
	}
	if err := t.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.types[t.ID]; exists {
		return fmt.Errorf("object type %s already registered", t.ID)
	}
	s.types[t.ID] = t
	return nil
}

func (s *Scanner) Spawn(typeID ObjectTypeID, id InstanceID, initial StateID, props map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.types[typeID]
	if !ok {
		return fmt.Errorf("unknown object type %s", typeID)
	}
	if _, exists := s.instances[id]; exists {
		return fmt.Errorf("instance %s already exists", id)
	}
	if initial == "" {
		initial = t.Initial
	}
	instProps := make(map[string]any, len(props))
	for k, v := range props {
		instProps[k] = v
	}
	s.instances[id] = &scanInstance{
		id:        id,
		typeID:    typeID,
		state:     initial,
		enteredAt: s.now,
		props:     instProps,
		links:     make(map[InstanceID]bool),
		history:   NewHistory(initial, s.now),
	}
	return nil
}

func (s *Scanner) AddLink(from, to InstanceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[from]; ok {
		inst.links[to] = true
	}
}

func (s *Scanner) RemoveLink(from, to InstanceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[from]; ok {
		delete(inst.links, to)
	}
}

func (s *Scanner) propOf(id InstanceID, key string) (any, bool) {
	inst, ok := s.instances[id]
	if !ok {
		return nil, false
	}
	v, ok := inst.props[key]
	return v, ok
}

func (s *Scanner) hasLink(from, to InstanceID) bool {
	inst, ok := s.instances[from]
	return ok && inst.links[to]
}

func (s *Scanner) guardContext(inst *scanInstance) GuardContext {
	return GuardContext{store: s, Self: inst.id}
}

// Tick advances virtual time by one scan interval and then keeps sweeping
// all instances until a sweep makes no progress. Sweep order is by instance
// id; because cascades are processed inline and sweeps repeat to fixpoint,
// the final state is order-independent.
func (s *Scanner) Tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.After(s.now) {
		s.now = now
	} else if s.interval > 0 {
		s.now = s.now.Add(s.interval)
		if now.After(s.now) {
			s.now = now
		}
	}
	var steps []SettlementStep
	for changed := true; changed; {
		changed = false
		ids := make([]InstanceID, 0, len(s.instances))
		for id := range s.instances {
			ids = append(ids, id)
		}
		sortInstanceIDs(ids)
		for _, id := range ids {
			if newSteps, ok := s.advanceOne(s.instances[id]); ok {
				steps = append(steps, newSteps...)
				changed = true
			}
		}
	}
	if s.logger != nil {
		s.logger.Log(SettlementInput{At: s.now, Trigger: "scan-tick", Root: "*"}, steps, nil)
	}
}

// advanceOne advances at most one due time transition of inst and handles its
// cascade inline. A failed guard simply leaves the instance where it is; the
// next tick re-evaluates with the same state entry time.
func (s *Scanner) advanceOne(inst *scanInstance) ([]SettlementStep, bool) {
	t := s.types[inst.typeID]
	tr := t.TimeTransitionFrom(inst.state)
	if tr == nil {
		return nil, false
	}
	due := inst.enteredAt.Add(tr.Duration)
	if s.now.Before(due) {
		return nil, false
	}
	if tr.Guard != nil && !tr.Guard(s.guardContext(inst)) {
		return nil, false
	}
	s.commit(inst, tr, EntryTime, due)
	step := SettlementStep{
		Instance:   inst.id,
		Kind:       EntryTime,
		Transition: tr.ID,
		From:       tr.From,
		To:         tr.To,
		DueAt:      due,
		LogicalAt:  due,
		Fired:      true,
		Reason:     "scan found elapsed duration",
	}
	out := []SettlementStep{step}
	if tr.Cascade != nil {
		if casStep, ok := s.forceCascade(inst, tr, due); ok {
			out = append(out, casStep)
		}
	}
	return out, true
}

func (s *Scanner) forceCascade(src *scanInstance, tr *Transition, logicalAt time.Time) (SettlementStep, bool) {
	targetID, ok := tr.Cascade.Target(s.guardContext(src))
	if !ok {
		return SettlementStep{}, false
	}
	target, exists := s.instances[targetID]
	if !exists {
		return SettlementStep{}, false
	}
	forced := s.types[target.typeID].FindTransition(tr.Cascade.Transition)
	if forced == nil || forced.From != target.state {
		return SettlementStep{}, false
	}
	s.commit(target, forced, EntryCascade, logicalAt)
	step := SettlementStep{
		Instance:   targetID,
		Kind:       EntryCascade,
		Transition: forced.ID,
		From:       forced.From,
		To:         forced.To,
		LogicalAt:  logicalAt,
		Fired:      true,
	}
	if forced.Cascade != nil {
		if nextStep, ok := s.forceCascade(target, forced, logicalAt); ok {
			// Caller logs only the primary step; the onward cascade becomes
			// visible on the next fixpoint sweep through target state.
			_ = nextStep
		}
	}
	return step, true
}

func (s *Scanner) commit(inst *scanInstance, tr *Transition, kind EntryKind, logicalAt time.Time) {
	entry := Entry{
		Kind:       kind,
		Transition: tr.ID,
		From:       inst.state,
		To:         tr.To,
		At:         logicalAt,
		SettledAt:  s.now,
	}
	if kind == EntryTime {
		entry.DueAt = logicalAt
	}
	inst.history.Append(entry)
	inst.state = tr.To
	if logicalAt.After(inst.enteredAt) {
		inst.enteredAt = logicalAt
	}
	if tr.Effect != nil {
		for k, v := range tr.Effect.SetProps {
			inst.props[k] = v
		}
		for _, target := range tr.Effect.AddLinks {
			inst.links[target] = true
		}
		for _, target := range tr.Effect.RemoveLinks {
			delete(inst.links, target)
		}
	}
}

// ApplyAction evaluates an explicit action against the post-scan state. It
// never back-dates: the entry takes the tick time.
func (s *Scanner) ApplyAction(id InstanceID, trID TransitionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return false
	}
	tr := s.types[inst.typeID].FindTransition(trID)
	if tr == nil || tr.Kind != KindActionTransition || tr.From != inst.state {
		return false
	}
	if tr.Guard != nil && !tr.Guard(s.guardContext(inst)) {
		return false
	}
	s.commit(inst, tr, EntryAction, s.now)
	return true
}

func (s *Scanner) State(id InstanceID) StateID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[id]; ok {
		return inst.state
	}
	return ""
}

func (s *Scanner) StateAt(id InstanceID, at time.Time) StateID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[id]; ok {
		return inst.history.Replay(at)
	}
	return ""
}

// Now returns the scanner's virtual time.
func (s *Scanner) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}
