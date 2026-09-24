// Package omap manages outer keys on top of the dependency-free lww state.
// It validates operations, applies them and exposes the filtered view.
// Dependency direction: omap -> lww only. An OMap is not safe for
// concurrent use itself; the api package serializes access.
package omap

import (
	"errors"

	"ontology/lww"
)

// Distinct, decidable sentinel errors for the three rejected operations.
var (
	ErrEmptyOuterKey = errors.New("omap: outer key must not be empty")
	ErrEmptyInnerKey = errors.New("omap: inner key must not be empty")
	ErrNonPositiveTS = errors.New("omap: timestamp must be positive")
)

// Kind selects what an Op does.
type Kind int

const (
	Put      Kind = iota // write one inner entry
	DelOuter             // raise the outer tombstone
)

// Op is one replicated operation.
type Op struct {
	Kind  Kind
	Outer string
	Inner string // used only by Put
	Value int    // used only by Put
	TS    int64
	Rep   string // used only by Put
}

// OMap is the outer-key manager.
type OMap struct {
	state *lww.Map
	// checked counts the outer keys inspected during the most recent Apply.
	// A hash-table lookup touches exactly one bucket, so it is the constant
	// 1 regardless of how many outer keys exist. It is deliberately
	// unexported and reachable from no exported method.
	checked int
}

// New returns an empty OMap.
func New() *OMap { return &OMap{state: lww.New()} }

// validate is run before any state is touched, so a rejected op leaves no
// trace whatsoever.
func (m *OMap) validate(op Op) error {
	if op.Outer == "" {
		return ErrEmptyOuterKey
	}
	if op.TS <= 0 {
		return ErrNonPositiveTS
	}
	if op.Kind == Put && op.Inner == "" {
		return ErrEmptyInnerKey
	}
	return nil
}

// Apply validates then applies op. Rejected ops fail wholesale and change
// nothing. Hash location by outer key inspects exactly one outer key; a
// rejected op is stopped during validation, before any stored outer key is
// inspected, so checked stays 0 in that case.
func (m *OMap) Apply(op Op) error {
	if err := m.validate(op); err != nil {
		m.checked = 0
		return err
	}
	m.checked = 1
	switch op.Kind {
	case Put:
		m.state.Put(op.Outer, op.Inner, op.Value, op.TS, op.Rep)
	case DelOuter:
		m.state.DelOuter(op.Outer, op.TS)
	}
	return nil
}

// Merge incrementally merges other into m (tombstones max, inner union by
// keyed LWW). It is not an Apply and therefore does not touch checked.
func (m *OMap) Merge(other *OMap) { m.state.Merge(other.state) }

// CloneState returns a deep copy of the lww state of other, for merging
// under a single lock in the api package without holding other's lock.
func (m *OMap) CloneState(other *OMap) { m.state.Merge(other.state.Clone()) }

// View returns the filtered view (fresh deep copy).
func (m *OMap) View() map[string]map[string]int { return m.state.View() }

// Tomb reports the tombstone timestamp of outer key o and whether the key
// exists (a key carrying only a tombstone still exists).
func (m *OMap) Tomb(o string) (int64, bool) { return m.state.Tomb(o) }

// LastApplyConstantTime reports whether the most recent Apply located its
// outer key without scanning the stored outer keys. It exposes only a
// boolean bound, never the counter's value, so the unexported counter stays
// out of the public interface.
func (m *OMap) LastApplyConstantTime() bool { return m.checked <= constantCheckBound }

// constantCheckBound is an m-independent small constant: hash location
// inspects one bucket regardless of the number of stored outer keys.
const constantCheckBound = 1
