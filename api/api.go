// Package api is the public face of the async I/O operator buffer.
package api

import (
	"strconv"
	"sync"

	"ontology/aentry"
	"ontology/aqueue"
)

// Mode selects ordered or unordered emission.
type Mode int

const (
	Ordered Mode = iota
	Unordered
)

// AsyncOp is the operator; all methods are safe for concurrent use.
type AsyncOp struct {
	mu      sync.Mutex
	q       queue
	emitted []aentry.Entry
}

type queue interface {
	In(id string) ([]aentry.Entry, error)
	Watermark(t int64) ([]aentry.Entry, error)
	Complete(id string) ([]aentry.Entry, error)
	Occupancy() int
}

// New returns an operator with the given mode and capacity.
func New(mode Mode, capN int) *AsyncOp {
	if mode == Ordered {
		return &AsyncOp{q: aqueue.NewOrdered(capN)}
	}
	return &AsyncOp{q: aqueue.NewUnordered(capN)}
}

func (a *AsyncOp) do(f func() ([]aentry.Entry, error)) ([]aentry.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, err := f()
	a.emitted = append(a.emitted, out...)
	return out, err
}

// In feeds an element (id non-empty and unique, occupancy below capacity).
func (a *AsyncOp) In(id string) ([]aentry.Entry, error) {
	return a.do(func() ([]aentry.Entry, error) { return a.q.In(id) })
}

// Watermark feeds a watermark strictly exceeding every previous one.
func (a *AsyncOp) Watermark(t int64) ([]aentry.Entry, error) {
	return a.do(func() ([]aentry.Entry, error) { return a.q.Watermark(t) })
}

// Complete declares the element's request finished (exactly once).
func (a *AsyncOp) Complete(id string) ([]aentry.Entry, error) {
	return a.do(func() ([]aentry.Entry, error) { return a.q.Complete(id) })
}

// Emitted returns all outputs so far, in emission order.
func (a *AsyncOp) Emitted() []aentry.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]aentry.Entry(nil), a.emitted...)
}

// Occupancy returns the number of accepted elements not yet output.
func (a *AsyncOp) Occupancy() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.q.Occupancy()
}

// Step is one input operation for replaying scenarios.
type Step struct {
	Op string // "in", "wm" or "complete"
	ID string
	T  int64
}

func (a *AsyncOp) step(s Step) ([]aentry.Entry, error) {
	switch s.Op {
	case "in":
		return a.In(s.ID)
	case "wm":
		return a.Watermark(s.T)
	}
	return a.Complete(s.ID)
}
