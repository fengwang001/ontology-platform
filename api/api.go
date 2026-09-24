// Package api is the public facade of the upsert-to-retract normalizer:
// New / Apply / Log / Snapshot / SelfCheck. It depends only on norm.
package api

import (
	"ontology/norm"
	"ontology/rfold"
)

// Op and Change are aliases of the wire types, so callers never import the
// inner packages to use the facade.
type (
	// Op is one upstream Upsert/Delete operation.
	Op = rfold.Op
	// Change is one downstream +(insert) / -(retract) changelog entry.
	Change = rfold.Change
	// OpKind is the operation kind (Upsert or Delete).
	OpKind = rfold.OpKind
	// ChangeKind is the changelog entry kind (retract or insert).
	ChangeKind = rfold.ChangeKind
)

// Operation and change constants re-exported for facade callers.
const (
	OpUpsert   = rfold.OpUpsert
	OpDelete   = rfold.OpDelete
	ChgRetract = rfold.ChgRetract
	ChgInsert  = rfold.ChgInsert
)

// Decidable sentinel errors; the three failure classes stay distinct.
var (
	ErrEmptyKey    = rfold.ErrEmptyKey
	ErrInvalidOp   = rfold.ErrInvalidOp
	ErrTooManyKeys = norm.ErrTooManyKeys
)

// Normalizer applies batches atomically against in-process state.
type Normalizer struct {
	n *norm.Normalizer
}

// New builds a Normalizer whose batches may leave at most maxKeys live keys.
func New(maxKeys int) *Normalizer {
	return &Normalizer{n: norm.New(maxKeys)}
}

// Apply folds one batch and returns its (possibly empty) change entries. A
// rejected batch changes nothing and returns one of the three sentinel errors.
func (x *Normalizer) Apply(batch []Op) ([]Change, error) {
	return x.n.Apply(batch)
}

// Log returns a copy of every change emitted so far, in emission order.
func (x *Normalizer) Log() []Change { return x.n.Log() }

// Snapshot returns a copy of the current live table.
func (x *Normalizer) Snapshot() map[string]int64 { return x.n.Snapshot() }

// SelfCheck runs the built-in seven-batch, invariant and complexity checks.
func SelfCheck() error { return norm.SelfCheck() }
