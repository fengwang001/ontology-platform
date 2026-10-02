// Package plancache implements a prepared-statement plan cache selector.
//
// For each registered statement the selector decides, on every execution,
// whether to run a custom-planned execution (Custom), to build a generic
// plan (BuildGeneric), or to reuse the generic plan (UseGeneric). The
// decision is driven by per-statement statistics: the number of custom
// executions c, the total custom cost sum, and the generic plan cost g.
package plancache

import (
	"fmt"
	"math/big"
	"sync"
)

// MaxCost is the largest cost value accepted by ReportCustom and
// ReportGeneric (inclusive).
const MaxCost uint64 = 1 << 40

// Decision is the outcome of Next.
type Decision int

const (
	// Custom means: execute with a custom plan, then call ReportCustom.
	Custom Decision = iota
	// BuildGeneric means: build the generic plan, then call ReportGeneric.
	BuildGeneric
	// UseGeneric means: execute with the generic plan, then call DoneGeneric.
	UseGeneric
)

func (d Decision) String() string {
	switch d {
	case Custom:
		return "Custom"
	case BuildGeneric:
		return "BuildGeneric"
	case UseGeneric:
		return "UseGeneric"
	default:
		return fmt.Sprintf("Decision(%d)", int(d))
	}
}

// Reason identifies why an operation was rejected.
type Reason int

const (
	ReasonInvalidK Reason = iota
	ReasonNegativeP
	ReasonEmptyName
	ReasonAlreadyExists
	ReasonNotFound
	ReasonPendingExists
	ReasonNoPending
	ReasonWrongPendingKind
	ReasonCostOutOfRange
	ReasonVersionNotIncreasing
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidK:
		return "K must be at least 1"
	case ReasonNegativeP:
		return "P must not be negative"
	case ReasonEmptyName:
		return "statement name is empty"
	case ReasonAlreadyExists:
		return "statement already exists"
	case ReasonNotFound:
		return "statement does not exist"
	case ReasonPendingExists:
		return "statement already has a pending decision"
	case ReasonNoPending:
		return "statement has no pending decision"
	case ReasonWrongPendingKind:
		return "pending decision has a different kind"
	case ReasonCostOutOfRange:
		return "cost is out of range [0, 2^40]"
	case ReasonVersionNotIncreasing:
		return "version must be greater than the current version"
	default:
		return fmt.Sprintf("Reason(%d)", int(r))
	}
}

// Error describes a rejected operation. Rejected operations never change
// any state.
type Error struct {
	Op     string
	Name   string
	Reason Reason
}

func (e *Error) Error() string {
	if e.Name == "" {
		return fmt.Sprintf("plancache: %s: %s", e.Op, e.Reason)
	}
	return fmt.Sprintf("plancache: %s %q: %s", e.Op, e.Name, e.Reason)
}

type pendingKind int

const (
	pendingNone pendingKind = iota
	pendingCustom
	pendingBuildGeneric
	pendingUseGeneric
)

type stmtState struct {
	mu      sync.Mutex
	version int64
	c       uint64
	sum     *big.Int
	g       uint64
	hasG    bool
	pending pendingKind
}

// Stats is a consistent snapshot of a statement's statistics.
type Stats struct {
	Version int64
	C       uint64
	Sum     *big.Int
	G       uint64
	HasG    bool
}

// Selector chooses between custom and generic plans for prepared
// statements. It is safe for concurrent use; operations on different
// statements proceed in parallel, and operations on the same statement
// behave as if executed in some serial order.
type Selector struct {
	mu      sync.RWMutex
	k       int64
	p       int64
	version int64
	stmts   map[string]*stmtState
}

// New creates a Selector with custom-plan trial count k (>= 1) and fixed
// per-custom-planning overhead p (>= 0).
func New(k, p int64) (*Selector, error) {
	if k < 1 {
		return nil, &Error{Op: "New", Reason: ReasonInvalidK}
	}
	if p < 0 {
		return nil, &Error{Op: "New", Reason: ReasonNegativeP}
	}
	return &Selector{
		k:     k,
		p:     p,
		stmts: make(map[string]*stmtState),
	}, nil
}

// Prepare registers a statement at the current schema version.
func (s *Selector) Prepare(name string) error {
	if name == "" {
		return &Error{Op: "Prepare", Reason: ReasonEmptyName}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stmts[name]; ok {
		return &Error{Op: "Prepare", Name: name, Reason: ReasonAlreadyExists}
	}
	s.stmts[name] = &stmtState{version: s.version, sum: new(big.Int)}
	return nil
}

// decide computes the next decision from st's statistics. Callers must
// hold st.mu. The comparison g*c < sum + P*c uses exact big-integer
// arithmetic and cannot overflow.
func (s *Selector) decide(st *stmtState) Decision {
	if st.c < uint64(s.k) {
		return Custom
	}
	if !st.hasG {
		return BuildGeneric
	}
	lhs := new(big.Int).Mul(new(big.Int).SetUint64(st.g), new(big.Int).SetUint64(st.c))
	rhs := new(big.Int).Add(st.sum, new(big.Int).Mul(big.NewInt(s.p), new(big.Int).SetUint64(st.c)))
	if lhs.Cmp(rhs) < 0 {
		return UseGeneric
	}
	return Custom
}

// Next returns the decision for the next execution of name and records it
// as the statement's pending decision.
func (s *Selector) Next(name string) (Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stmts[name]
	if !ok {
		return Custom, &Error{Op: "Next", Name: name, Reason: ReasonNotFound}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pending != pendingNone {
		return Custom, &Error{Op: "Next", Name: name, Reason: ReasonPendingExists}
	}
	d := s.decide(st)
	switch d {
	case Custom:
		st.pending = pendingCustom
	case BuildGeneric:
		st.pending = pendingBuildGeneric
	case UseGeneric:
		st.pending = pendingUseGeneric
	}
	return d, nil
}

// checkPending validates the report protocol for name and returns the
// locked statement state. On success the caller owns st.mu and must
// unlock it.
func (s *Selector) checkPending(op, name string, cost uint64, checkCost bool, want pendingKind) (*stmtState, error) {
	s.mu.RLock()
	st, ok := s.stmts[name]
	if !ok {
		s.mu.RUnlock()
		return nil, &Error{Op: op, Name: name, Reason: ReasonNotFound}
	}
	st.mu.Lock()
	s.mu.RUnlock()
	if st.pending == pendingNone {
		st.mu.Unlock()
		return nil, &Error{Op: op, Name: name, Reason: ReasonNoPending}
	}
	if st.pending != want {
		st.mu.Unlock()
		return nil, &Error{Op: op, Name: name, Reason: ReasonWrongPendingKind}
	}
	if checkCost && cost > MaxCost {
		st.mu.Unlock()
		return nil, &Error{Op: op, Name: name, Reason: ReasonCostOutOfRange}
	}
	return st, nil
}

// ReportCustom reports the cost of a Custom execution.
func (s *Selector) ReportCustom(name string, cost uint64) error {
	st, err := s.checkPending("ReportCustom", name, cost, true, pendingCustom)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	st.c++
	st.sum.Add(st.sum, new(big.Int).SetUint64(cost))
	st.pending = pendingNone
	return nil
}

// ReportGeneric reports the cost of the generic plan after BuildGeneric.
func (s *Selector) ReportGeneric(name string, cost uint64) error {
	st, err := s.checkPending("ReportGeneric", name, cost, true, pendingBuildGeneric)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	st.g = cost
	st.hasG = true
	st.pending = pendingNone
	return nil
}

// DoneGeneric completes a UseGeneric execution without changing statistics.
func (s *Selector) DoneGeneric(name string) error {
	st, err := s.checkPending("DoneGeneric", name, 0, false, pendingUseGeneric)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	st.pending = pendingNone
	return nil
}

// Bump raises the schema version to v and resets every statement whose
// registration version is older than v.
func (s *Selector) Bump(v int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v <= s.version {
		return &Error{Op: "Bump", Reason: ReasonVersionNotIncreasing}
	}
	s.version = v
	for _, st := range s.stmts {
		if st.version >= v {
			continue
		}
		st.mu.Lock()
		st.c = 0
		st.sum.SetInt64(0)
		st.g = 0
		st.hasG = false
		st.pending = pendingNone
		st.version = v
		st.mu.Unlock()
	}
	return nil
}

// Drop removes a statement.
func (s *Selector) Drop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stmts[name]; !ok {
		return &Error{Op: "Drop", Name: name, Reason: ReasonNotFound}
	}
	delete(s.stmts, name)
	return nil
}

// StatsOf returns a snapshot of a statement's statistics.
func (s *Selector) StatsOf(name string) (Stats, bool) {
	s.mu.RLock()
	st, ok := s.stmts[name]
	s.mu.RUnlock()
	if !ok {
		return Stats{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return Stats{
		Version: st.version,
		C:       st.c,
		Sum:     new(big.Int).Set(st.sum),
		G:       st.g,
		HasG:    st.hasG,
	}, true
}
