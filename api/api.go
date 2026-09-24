// Package api is the public face of the barrier-aligning sum operator.
// It depends on align; all state lives in process memory.
package api

import (
	"ontology/align"
)

// Rejection reasons, mutually distinct sentinel errors.
var (
	ErrBadElem      = align.ErrBadElem
	ErrBarrierOrder = align.ErrBarrierOrder
	ErrBufferFull   = align.ErrBufferFull
)

// Item is one input element; Out is one output-stream element.
type Item = align.Item
type Out = align.Out

// Element kinds.
const (
	Record  = align.Record
	Barrier = align.Barrier
)

// Op is a two-input summing operator with aligned checkpoints.
// Methods are safe for concurrent use.
type Op struct {
	a *align.Align
}

// New returns an operator allowing at most maxBuffered buffered elements.
func New(maxBuffered int) *Op { return &Op{a: align.New(maxBuffered)} }

// Push applies one batch in order and returns the output fragment it
// produced. Any rejected element fails the whole batch without effect.
func (o *Op) Push(items []Item) ([]Out, error) { return o.a.Push(items) }

// Snapshot returns a copy of checkpoint snapshot n.
func (o *Op) Snapshot(n int64) (map[string]int64, bool) { return o.a.Snapshot(n) }

// State returns a copy of the current sum.
func (o *Op) State() map[string]int64 { return o.a.State() }

// SelfCheck verifies the invariants on built-in input sequences.
func (o *Op) SelfCheck() error { return align.SelfCheck() }
