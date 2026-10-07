package ontology

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrNoImplementation = errors.New("no implementation on type or any ancestor")
	ErrExplicitlyWaived = errors.New("type explicitly waived direct handling and no ancestor implements")
	ErrObjectRevoked    = errors.New("object revoked concurrently during lookup")
	ErrPrecondition     = errors.New("implementation precondition failed")
	ErrPostcondition    = errors.New("implementation postcondition failed")
)

// DispatchErrorClass enumerates the four required failure classes.
type DispatchErrorClass int

const (
	// ClassNoDispatch: neither the concrete type nor any ancestor registered.
	ClassNoDispatch DispatchErrorClass = iota
	// ClassWaivedNoDispatch: the concrete type explicitly waived direct
	// handling AND no ancestor registered. Distinguishable from ClassNoDispatch.
	ClassWaivedNoDispatch
	// ClassObjectRevoked: the object was concurrently revoked during lookup.
	ClassObjectRevoked
	// ClassCondition: an implementation's pre/post condition did not pass.
	ClassCondition
)

// PathBasis explains why one hop produced its outcome.
type PathBasis int

const (
	BasisDirect    PathBasis = iota // hit a direct registration on this type
	BasisInherited                  // this hop had no usable registration, skipped
	BasisWaived                     // this hop explicitly waived direct handling
	BasisAbsent                     // this hop never registered anything
)

// PathStep records one hop of the resolution walk, for post-hoc verification.
type PathStep struct {
	Type  TypeID
	Basis PathBasis
}

// DispatchResult is the final outcome class of an invocation.
type DispatchResult int

const (
	ResultSucceeded DispatchResult = iota
	ResultFailed
)

// DispatchTrace is recorded per invocation for post-hoc verification.
type DispatchTrace struct {
	CallSeq      uint64 // global order position of the invocation initiation
	Action       ActionID
	ObjectID     string
	ConcreteType TypeID
	Path         []PathStep
	Hops         int // chain steps actually inspected (<= depth to hit)
	// HitBasis is direct vs inherited for the chosen generation.
	HitBasis   PathBasis
	OwnerType  TypeID
	GenSeq     uint64
	SawWaive   bool // whether the walk observed an explicit waive
	Result     DispatchResult
	ErrorClass DispatchErrorClass
}

// LookupHook allows tests to park a lookup at a specific hop so that
// concurrent revocation / replacement can be interleaved deterministically.
type LookupHook interface {
	BeforeHop(trace *DispatchTrace, hopIndex int)
}

// Dispatcher performs polymorphic dispatch. All initiations and all
// registrations linearize against one mutex, so the implementation a call
// uses is exactly the one visible at its single defined initiation point.
type Dispatcher struct {
	reg *Registry

	tracesMu sync.Mutex
	traces   []*DispatchTrace

	hook LookupHook
}

func NewDispatcher(r *Registry) *Dispatcher {
	return &Dispatcher{reg: r}
}

// SetHook installs a test-only interception point.
func (d *Dispatcher) SetHook(h LookupHook) { d.hook = h }

// Traces returns a copy of every recorded invocation trace.
func (d *Dispatcher) Traces() []*DispatchTrace {
	d.tracesMu.Lock()
	defer d.tracesMu.Unlock()
	out := make([]*DispatchTrace, len(d.traces))
	copy(out, d.traces)
	return out
}

func (d *Dispatcher) record(t *DispatchTrace) {
	d.tracesMu.Lock()
	d.traces = append(d.traces, t)
	d.tracesMu.Unlock()
}

// Invoke performs one call.
//
// Initiation point (the single determinate "call started" instant) is the
// moment the dispatcher acquires the registry mutex and assigns this call its
// global callSeq. Everything before it is caller-side preparation; the
// generation resolved under the mutex is the one the call executes to
// completion, regardless of replacements that land after initiation.
func (d *Dispatcher) Invoke(ctx context.Context, obj *Instance, action ActionID, input any) (any, *DispatchTrace, error) {
	trace := &DispatchTrace{
		Action:       action,
		ObjectID:     obj.ID,
		ConcreteType: obj.Type.ID,
	}

	// Resolution runs entirely under the registry mutex. It cannot be
	// interleaved with a replacement, so the pinned generation is unambiguous.
	d.reg.mu.Lock()
	trace.CallSeq = d.reg.touchLocked()

	gen, owner, hitBasis, sawWaive, hops, _, cls, err := d.resolveLocked(obj, action, trace)
	if err != nil {
		d.reg.mu.Unlock()
		trace.Result = ResultFailed
		trace.ErrorClass = cls
		trace.Hops = hops
		trace.SawWaive = sawWaive
		d.record(trace)
		return nil, trace, err
	}

	// Structural dispatch success is now decided. Precondition still runs
	// inside the mutex window of initiation; it belongs to condition failures,
	// which can only occur after dispatch succeeded.
	if !gen.impl.Pre(input) {
		d.reg.mu.Unlock()
		trace.Hops = hops
		trace.SawWaive = sawWaive
		trace.OwnerType = owner
		trace.GenSeq = gen.seq
		trace.HitBasis = hitBasis
		trace.Result = ResultFailed
		trace.ErrorClass = ClassCondition
		d.record(trace)
		return nil, trace, ErrPrecondition
	}
	impl := gen.impl
	startSeq := gen.seq
	d.reg.mu.Unlock()

	trace.Hops = hops
	trace.SawWaive = sawWaive
	trace.OwnerType = owner
	trace.GenSeq = startSeq
	trace.HitBasis = hitBasis

	// Execution happens outside registry state: the pinned impl is immutable
	// and never swapped under us. Revocation after this point is the
	// implementation's own post-check responsibility, not the dispatcher's.
	output, execErr := impl.Execute(&ExecContext{Object: obj, StartedAt: startSeq}, input)
	if execErr != nil {
		trace.Result = ResultFailed
		trace.ErrorClass = ClassCondition
		d.record(trace)
		return nil, trace, execErr
	}
	if !impl.Post(input, output) {
		trace.Result = ResultFailed
		trace.ErrorClass = ClassCondition
		d.record(trace)
		return nil, trace, ErrPostcondition
	}
	if act := d.reg.actions[action]; act != nil && act.Postcondition != nil && !act.Postcondition(input, output) {
		trace.Result = ResultFailed
		trace.ErrorClass = ClassCondition
		d.record(trace)
		return nil, trace, ErrPostcondition
	}

	trace.Result = ResultSucceeded
	d.record(trace)
	return output, trace, nil
}

// resolveLocked walks [concrete, parent, ...] exactly once. It never consults
// the property-value lookup rules: implementation dispatch is an independent
// chain walk over the static type hierarchy only.
//
// It returns either a pinned generation, or a classified failure. Structural
// no-dispatch failures take precedence over concurrent revocation per the
// required ordering: undispatchable cases are judged before any logic runs.
func (d *Dispatcher) resolveLocked(obj *Instance, action ActionID, trace *DispatchTrace) (
	gen *generation,
	owner TypeID,
	hitBasis PathBasis,
	sawWaive bool,
	hops int,
	revokeAt int,
	cls DispatchErrorClass,
	err error,
) {
	chain := obj.Type.chain()
	var first *generation
	var firstOwner TypeID
	var firstBasis PathBasis
	revokeAt = -1

walk:
	for i, t := range chain {
		if d.hook != nil {
			d.hook.BeforeHop(trace, i)
		}
		hops = i + 1
		st, g := d.reg.snapshotLocked(action, t.ID)

		// Revocation is observed at each hop. A revoke landing during lookup
		// fails the whole call; we keep scanning only enough to establish
		// whether a structural hit would exist, because no-dispatch
		// classification must take precedence.
		if obj.Revoked() {
			revokeAt = i
		}

		switch st {
		case stateRegistered:
			trace.Path = append(trace.Path, PathStep{Type: t.ID, Basis: BasisDirect})
			if first == nil {
				first = g
				firstOwner = t.ID
				if i == 0 {
					firstBasis = BasisDirect
				} else {
					firstBasis = BasisInherited
				}
			}
			// First registered generation along the chain fixes both the
			// implementation and the walk length. If a revocation was
			// observed at or before this depth it was already captured; we
			// never inspect any hop deeper than the actual hit.
			break walk
		case stateWaived:
			sawWaive = true
			trace.Path = append(trace.Path, PathStep{Type: t.ID, Basis: BasisWaived})
		default:
			trace.Path = append(trace.Path, PathStep{Type: t.ID, Basis: BasisAbsent})
		}
	}

	if first == nil {
		// Two distinct no-dispatch states.
		if sawWaive {
			return nil, "", 0, sawWaive, hops, revokeAt, ClassWaivedNoDispatch, ErrExplicitlyWaived
		}
		return nil, "", 0, sawWaive, hops, revokeAt, ClassNoDispatch, ErrNoImplementation
	}
	if revokeAt >= 0 {
		return nil, "", 0, sawWaive, hops, revokeAt, ClassObjectRevoked, ErrObjectRevoked
	}
	return first, firstOwner, firstBasis, sawWaive, hops, revokeAt, 0, nil
}
