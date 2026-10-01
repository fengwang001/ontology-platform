// Package plancache implements a plan-cache selector for prepared
// statements. It chooses between custom (per-binding) plans and a single
// generic plan based on observed execution counts and plan costs.
package plancache

import (
	"math/big"
	"sync"
)

// Decision is the verdict returned by Next.
type Decision int

const (
	// Custom means a custom plan must be planned and reported.
	Custom Decision = iota
	// BuildGeneric means the generic plan must be built once and reported.
	BuildGeneric
	// UseGeneric means the existing generic plan is reused; confirm with
	// DoneGeneric.
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
		return "Unknown"
	}
}

// MaxCost is the largest legal reported cost (2^40).
const MaxCost int64 = 1 << 40

// Selector chooses between custom and generic plans.
//
// All comparisons use arbitrary-precision integers, so values up to
// cost 2^40 with custom counts far above 2^20 never overflow.
type Selector struct {
	mu      sync.RWMutex
	k       *big.Int
	p       *big.Int
	version uint64
	stmts   map[string]*stmt
}

type stmt struct {
	// op serializes per-statement operations; concurrent Next calls on the
	// same statement leave exactly one winner.
	op sync.Mutex

	registeredVersion uint64

	// c is the number of custom-plan executions observed since the last
	// architecture reset; sum is the sum of their costs.
	c   *big.Int
	sum *big.Int
	// g is the generic plan cost, or nil while still unknown.
	g *big.Int

	// pending is zero when no decision is outstanding.
	pending Decision
}

func newStmt(version uint64) *stmt {
	return &stmt{
		registeredVersion: version,
		c:                 new(big.Int),
		sum:               new(big.Int),
		pending:           Decision(-1),
	}
}

// New creates a Selector. K is the number of mandatory custom-plan trials
// (K >= 1); P is the fixed per-custom-plan overhead (P >= 0).
func New(k int, p int64) (*Selector, error) {
	if k < 1 {
		return nil, ErrKTooSmall
	}
	if p < 0 {
		return nil, ErrPNegative
	}
	return &Selector{
		k:       big.NewInt(int64(k)),
		p:       big.NewInt(p),
		version: 0,
		stmts:   make(map[string]*stmt),
	}, nil
}

// Prepare registers a statement at the current architecture version.
func (s *Selector) Prepare(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stmts[name]; ok {
		return ErrNameExists
	}
	s.stmts[name] = newStmt(s.version)
	return nil
}

// lockStmt returns the statement and its operation lock, holding the latter
// while the registry read lock stays held. This serializes against Drop/Bump
// (which need the registry write lock) so a statement can never vanish or be
// reset between lookup and use.
func (s *Selector) lockStmt(name string) (*stmt, error) {
	s.mu.RLock()
	st, ok := s.stmts[name]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrStatementNotFound
	}
	st.op.Lock()
	s.mu.RUnlock()
	return st, nil
}

// decide evaluates the rule without mutating state:
//
//	c < K                 -> Custom
//	g unknown             -> BuildGeneric
//	g*c < sum + P*c       -> UseGeneric
//	otherwise             -> Custom
func (s *Selector) decide(st *stmt) Decision {
	if st.c.Cmp(s.k) < 0 {
		return Custom
	}
	if st.g == nil {
		return BuildGeneric
	}
	left := new(big.Int).Mul(st.g, st.c)
	right := new(big.Int).Add(st.sum, new(big.Int).Mul(s.p, st.c))
	if left.Cmp(right) < 0 {
		return UseGeneric
	}
	return Custom
}

// Next returns the decision for the named statement and marks it pending.
// Repeated Next calls while a decision is pending are rejected; once the
// pending decision is paired with its report, calling Next again yields the
// same rule-driven decision for unchanged statistics (UseGeneric stays
// UseGeneric until statistics change).
func (s *Selector) Next(name string) (Decision, error) {
	st, err := s.lockStmt(name)
	if err != nil {
		return 0, err
	}
	defer st.op.Unlock()
	if st.pending >= 0 {
		return 0, ErrPendingExists
	}
	d := s.decide(st)
	st.pending = d
	return d, nil
}

func checkCost(cost int64) error {
	if cost < 0 || cost > MaxCost {
		return ErrCostOutOfRange
	}
	return nil
}

// ReportCustom records a custom-plan execution. Legal only while a Custom
// decision is pending. An out-of-range cost leaves the pending decision and
// every statistic untouched.
func (s *Selector) ReportCustom(name string, cost int64) error {
	st, err := s.lockStmt(name)
	if err != nil {
		return err
	}
	defer st.op.Unlock()
	if st.pending != Custom {
		return ErrPendingMismatch
	}
	if err := checkCost(cost); err != nil {
		return err
	}
	st.c.Add(st.c, big.NewInt(1))
	st.sum.Add(st.sum, big.NewInt(cost))
	st.pending = Decision(-1)
	return nil
}

// ReportGeneric records the one-time generic plan cost. Legal only while a
// BuildGeneric decision is pending. An out-of-range cost leaves the pending
// decision and every statistic untouched.
func (s *Selector) ReportGeneric(name string, cost int64) error {
	st, err := s.lockStmt(name)
	if err != nil {
		return err
	}
	defer st.op.Unlock()
	if st.pending != BuildGeneric {
		return ErrPendingMismatch
	}
	if err := checkCost(cost); err != nil {
		return err
	}
	st.g = big.NewInt(cost)
	st.pending = Decision(-1)
	return nil
}

// DoneGeneric confirms a UseGeneric decision was executed. It clears the
// pending flag and changes no statistics.
func (s *Selector) DoneGeneric(name string) error {
	st, err := s.lockStmt(name)
	if err != nil {
		return err
	}
	defer st.op.Unlock()
	if st.pending != UseGeneric {
		return ErrPendingMismatch
	}
	st.pending = Decision(-1)
	return nil
}

// Bump raises the architecture version to v. Every statement registered at a
// version lower than v is reset to its initial trial state and re-registered
// at v; statements already at v keep all statistics.
func (s *Selector) Bump(v uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v <= s.version {
		return ErrVersionNotGreater
	}
	for _, st := range s.stmts {
		if st.registeredVersion < v {
			st.op.Lock()
			st.c.SetInt64(0)
			st.sum.SetInt64(0)
			st.g = nil
			st.pending = Decision(-1)
			st.registeredVersion = v
			st.op.Unlock()
		}
	}
	s.version = v
	return nil
}

// Drop removes a statement; later operations on the name report
// ErrStatementNotFound.
func (s *Selector) Drop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stmts[name]; !ok {
		return ErrStatementNotFound
	}
	delete(s.stmts, name)
	return nil
}
