// Package api is the public face of the asynchronous checkpoint barrier
// snapshot: New, Feed, Sum, Snapshot, SelfCheck. It depends only on snap.
package api

import (
	"sync"

	"ontology/snap"
)

// Event is one Rec(ch, delta) or Bar(ch, id).
type Event = snap.Event

// The four failure modes are distinct, decidable sentinel errors.
var (
	ErrChannels     = snap.ErrChannels
	ErrChannelRange = snap.ErrChannelRange
	ErrBarrierID    = snap.ErrBarrierID
	ErrBarrierOrder = snap.ErrBarrierOrder
)

// Rec builds a record carrying delta on channel ch.
// Bar builds a barrier with id on channel ch.
var (
	Rec = snap.Rec
	Bar = snap.Bar
)

// Operator is the concurrency-safe aligned snapshot operator.
type Operator struct {
	mu sync.RWMutex
	e  *snap.Engine
}

// New builds an operator over channels input channels.
func New(channels int) (*Operator, error) {
	e, err := snap.New(channels)
	if err != nil {
		return nil, err
	}
	return &Operator{e: e}, nil
}

// Feed validates the batch as a whole and, only when every event is legal,
// applies it in global arrival order. A rejected batch changes no state.
func (o *Operator) Feed(evs []Event) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.e.Apply(evs)
}

// Sum returns the current accumulator value; safe for concurrent use.
func (o *Operator) Sum() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.e.Sum()
}

// Snapshot returns the snapshot taken when barrier id completed alignment.
func (o *Operator) Snapshot(id int) (int, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.e.Snapshot(id)
}

// SelfCheck runs the built-in checks over the eight-event stream, rejected
// batches and large channel counts; safe for concurrent invocation.
func SelfCheck() error { return snap.SelfCheck() }
