package ontology

import "sync"

// entryState distinguishes the three states a (action, type) slot can be in.
// stateAbsent and stateWaived must remain distinguishable: absent means no
// registration ever happened, waived means the type explicitly declared it
// does not want direct handling and inheritance lookup must continue upward.
type entryState int

const (
	stateAbsent entryState = iota
	stateRegistered
	stateWaived
)

// generation is one immutable version of a slot. In-flight calls pin a
// *generation; replacing registration appends a new generation and never
// mutates an old one, so in-flight calls execute the pre-replacement logic
// to completion even after the replacement becomes effective.
type generation struct {
	seq    uint64
	impl   Implementation
	relax  *Relaxation
	action ActionID
	owner  TypeID
}

// slot is the per (action, concrete type) registration state.
type slot struct {
	state entryState
	// current is the newest generation; nil unless state == stateRegistered.
	current *generation
	// history retains every generation so replacements remain auditable.
	history []*generation
}

// Registry holds versioned implementations per (action, concrete type) and
// produces the single global order of registration/waive operations.
type Registry struct {
	mu sync.Mutex
	// seq is the global linearization counter for every mutation.
	seq uint64
	// actions stores interface-level action declarations for audit access.
	actions map[ActionID]*Action
	table   map[ActionID]map[TypeID]*slot
	// mutationLog records every registration operation in global order.
	mutationLog []MutationRecord
}

// MutationRecord is one registration/waive operation in global total order.
type MutationRecord struct {
	Seq    uint64
	Action ActionID
	Type   TypeID
	Kind   MutationKind
	Gen    *generation
}

// MutationKind classifies a registry mutation.
type MutationKind int

const (
	MutationRegister MutationKind = iota
	MutationReplace
	MutationWaive
)

func NewRegistry() *Registry {
	return &Registry{
		actions: make(map[ActionID]*Action),
		table:   make(map[ActionID]map[TypeID]*slot),
	}
}

// DeclareAction records the generic interface declaration of an action.
func (r *Registry) DeclareAction(a *Action) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions[a.ID] = a
}

// Register installs the first implementation for (action, typ). It errors if
// a generation already exists; use Replace to swap running logic.
func (r *Registry) Register(action ActionID, typ TypeID, impl Implementation, relax *Relaxation) (*generation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreateSlot(action, typ)
	if s.state == stateRegistered {
		return nil, &RegistrationError{Action: action, Type: typ, Reason: reasonAlreadyRegistered}
	}
	return r.installLocked(action, typ, impl, relax, MutationRegister), nil
}

// Waive marks (action, typ) as explicitly giving up direct handling. Lookup
// will skip this layer and continue up the inheritance chain. Waive is a
// distinct state from "never registered" and stays auditable.
func (r *Registry) Waive(action ActionID, typ TypeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreateSlot(action, typ)
	s.state = stateWaived
	r.seq++
	r.mutationLog = append(r.mutationLog, MutationRecord{
		Seq: r.seq, Action: action, Type: typ, Kind: MutationWaive,
	})
}

// Replace swaps in a new generation for (action, typ). The old generation
// stays reachable: in-flight calls already resolved against it run it to
// completion, and audit compares old vs new behavior.
//
// If the new implementation relaxes rejection for any input that the old
// implementation rejected, a non-nil Relaxation is mandatory; otherwise
// registration is rejected at registration time (relax meta required).
func (r *Registry) Replace(action ActionID, typ TypeID, impl Implementation, relax *Relaxation, probes []any) (*generation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreateSlot(action, typ)
	if s.state != stateRegistered || s.current == nil {
		return nil, &RegistrationError{Action: action, Type: typ, Reason: reasonNoPriorGeneration}
	}
	if err := checkRelaxationRequired(r.actions[action], s.current, impl, relax, probes); err != nil {
		return nil, err
	}
	gen := r.installLocked(action, typ, impl, relax, MutationReplace)
	return gen, nil
}

func (r *Registry) installLocked(action ActionID, typ TypeID, impl Implementation, relax *Relaxation, kind MutationKind) *generation {
	r.seq++
	gen := &generation{seq: r.seq, impl: impl, relax: relax, action: action, owner: typ}
	s := r.getOrCreateSlot(action, typ)
	s.state = stateRegistered
	s.current = gen
	s.history = append(s.history, gen)
	r.mutationLog = append(r.mutationLog, MutationRecord{
		Seq: r.seq, Action: action, Type: typ, Kind: kind, Gen: gen,
	})
	return gen
}

func (r *Registry) getOrCreateSlot(action ActionID, typ TypeID) *slot {
	m, ok := r.table[action]
	if !ok {
		m = make(map[TypeID]*slot)
		r.table[action] = m
	}
	s, ok := m[typ]
	if !ok {
		s = &slot{state: stateAbsent}
		m[typ] = s
	}
	return s
}

// snapshotLocked returns the current generation and state for a slot.
func (r *Registry) snapshotLocked(action ActionID, typ TypeID) (entryState, *generation) {
	if m, ok := r.table[action]; ok {
		if s, ok := m[typ]; ok {
			return s.state, s.current
		}
	}
	return stateAbsent, nil
}

// MutationLog returns a copy of the global mutation order for verification.
func (r *Registry) MutationLog() []MutationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MutationRecord, len(r.mutationLog))
	copy(out, r.mutationLog)
	return out
}

// touchLocked issues the next position in the single global order shared by
// mutations and call initiations. Dispatcher resolution runs under the same
// mutex, so a call's initiation position interleaves with mutation positions
// in exactly the order in which they really took effect.
func (r *Registry) touchLocked() uint64 {
	r.seq++
	return r.seq
}

// Seq returns the current global order position.
func (r *Registry) Seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// RegistrationReason explains why a registration/replacement was rejected.
type RegistrationReason string

const (
	reasonAlreadyRegistered RegistrationReason = "implementation already registered; use Replace"
	reasonNoPriorGeneration RegistrationReason = "no prior generation to replace"
	reasonRelaxRequired     RegistrationReason = "replacement relaxes rejection but declares no relaxation scope"
)

// RegistrationError is returned when registration rules are violated.
type RegistrationError struct {
	Action ActionID
	Type   TypeID
	Reason RegistrationReason
}

func (e *RegistrationError) Error() string {
	return string(e.Reason) + " (action=" + string(e.Action) + ", type=" + string(e.Type) + ")"
}
