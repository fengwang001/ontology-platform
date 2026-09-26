// Package bp is the backpressure controller. It validates arguments,
// orchestrates the bq core, and turns a full queue into an explicit
// backpressure signal (false, nil) rather than an error. It depends only on bq.
package bp

import (
	"errors"

	"ontology/bq"
)

// Sentinel errors. The four failure kinds must be mutually distinct so callers
// can decide with errors.Is.
var (
	// ErrInvalidCapacity: configuration rejected at construction (capacity < 1).
	ErrInvalidCapacity = errors.New("bp: capacity must be >= 1")
	// ErrInvalidProduce: Produce called with n < 1.
	ErrInvalidProduce = errors.New("bp: Produce requires n >= 1")
	// ErrInvalidConsume: Consume called with n < 1.
	ErrInvalidConsume = errors.New("bp: Consume requires n >= 1")
	// ErrUnderflow: Consume asked for more elements than the queue holds.
	ErrUnderflow = errors.New("bp: Consume underflow: n > current count")
)

// Controller wraps a bq core with validation and backpressure semantics.
type Controller struct {
	q *bq.Queue
}

// New constructs a controller, rejecting non-positive capacity before any
// queue state exists.
func New(capacity int64) (*Controller, error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Controller{q: bq.New(capacity)}, nil
}

// Produce places n elements as one atomic batch.
//
//	n < 1           -> (false, ErrInvalidProduce), state untouched
//	count + n > cap -> (false, nil)                backpressure, nothing placed
//	otherwise       -> (true, nil)                 all n placed
func (c *Controller) Produce(n int64) (bool, error) {
	if n < 1 {
		return false, ErrInvalidProduce
	}
	return c.q.Produce(n), nil
}

// Consume removes n elements as one atomic batch.
//
//	n < 1    -> ErrInvalidConsume, state untouched
//	n > count-> ErrUnderflow, state untouched
//	otherwise-> nil and exactly n elements removed
//
// The core decides underflow while holding its lock, so the check and the
// removal are one atomic step.
func (c *Controller) Consume(n int64) error {
	if n < 1 {
		return ErrInvalidConsume
	}
	if !c.q.Consume(n) {
		return ErrUnderflow
	}
	return nil
}

// Count reports the number of held elements.
func (c *Controller) Count() int64 { return c.q.Count() }

// Free reports the number of free slots.
func (c *Controller) Free() int64 { return c.q.Free() }
