// Package bp is the backpressure controller: it validates arguments,
// orchestrates the bq core, and turns a full queue into a backpressure signal.
// It depends only on bq.
package bp

import (
	"errors"

	"ontology/bq"
)

// The four failures are distinct sentinel errors so callers can judge them.
var (
	// ErrBadCapacity is the configuration failure: capacity must be >= 1.
	ErrBadCapacity = errors.New("bp: capacity must be at least 1")
	// ErrIllegalProduce is a producer precondition failure: n must be >= 1.
	ErrIllegalProduce = errors.New("bp: produce amount must be at least 1")
	// ErrIllegalConsume is a consumer precondition failure: n must be >= 1.
	ErrIllegalConsume = errors.New("bp: consume amount must be at least 1")
	// ErrUnderflow is consuming more elements than the queue currently holds.
	ErrUnderflow = errors.New("bp: consume underflow")
)

// Controller validates calls and drives the underlying ring.
type Controller struct {
	ring *bq.Ring
	cap  int64
}

// NewController builds a controller, rejecting non-positive capacity.
func NewController(capacity int64) (*Controller, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Controller{ring: bq.New(capacity), cap: capacity}, nil
}

// Produce places all n elements or none. A full queue yields (false, nil):
// backpressure, the caller retries later. n <= 0 yields ErrIllegalProduce and
// changes no state.
func (c *Controller) Produce(n int64) (bool, error) {
	if n <= 0 {
		return false, ErrIllegalProduce
	}
	return c.ring.TryProduce(n), nil
}

// Consume removes all n elements or none. n <= 0 yields ErrIllegalConsume;
// n > Count yields ErrUnderflow. Neither failure changes state.
func (c *Controller) Consume(n int64) error {
	if n <= 0 {
		return ErrIllegalConsume
	}
	if !c.ring.TryConsume(n) {
		return ErrUnderflow
	}
	return nil
}

// Count returns the current number of elements.
func (c *Controller) Count() int64 { return c.ring.Count() }

// Free returns the remaining capacity.
func (c *Controller) Free() int64 { return c.ring.Free() }

// Capacity returns the fixed capacity.
func (c *Controller) Capacity() int64 { return c.cap }
