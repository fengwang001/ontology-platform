// Package api is the public entry point for the bounded queue. It depends
// only on bp (which depends on bq); the dependency direction never reverses.
package api

import (
	"errors"

	"ontology/bp"
)

// Sentinel errors are re-exported from bp so api is self-contained for
// callers while keeping a single identity per error (errors.Is keeps working).
var (
	ErrInvalidCapacity = bp.ErrInvalidCapacity // capacity < 1 at New
	ErrInvalidProduce  = bp.ErrInvalidProduce  // Produce n < 1
	ErrInvalidConsume  = bp.ErrInvalidConsume  // Consume n < 1
	ErrUnderflow       = bp.ErrUnderflow       // Consume n > Count
)

// Queue is the concurrency-safe bounded FIFO queue.
type Queue struct {
	c *bp.Controller
}

// New constructs a queue; capacity must be >= 1.
func New(capacity int64) (*Queue, error) {
	c, err := bp.New(capacity)
	if err != nil {
		return nil, err
	}
	return &Queue{c: c}, nil
}

// Produce places all n elements or none: (true, nil) on success,
// (false, nil) on backpressure (queue full), (false, err) on invalid n.
func (q *Queue) Produce(n int64) (bool, error) { return q.c.Produce(n) }

// Consume removes exactly n elements, or returns a sentinel and changes
// nothing (invalid n or underflow).
func (q *Queue) Consume(n int64) error { return q.c.Consume(n) }

// Count returns the number of held elements (0 <= Count <= capacity).
func (q *Queue) Count() int64 { return q.c.Count() }

// Free returns capacity - Count.
func (q *Queue) Free() int64 { return q.c.Free() }

// scOp is one step of a built-in SelfCheck sequence.
//
//	err != nil: the call must be rejected with exactly that sentinel
//	err == nil && accept: the call must succeed
//	err == nil && !accept (produce only): the call must backpressure
type scOp struct {
	prod   bool
	n      int64
	accept bool
	err    error
}

// SelfCheck replays built-in operation sequences and verifies all four
// invariants after every step: (1) equality with a naive explicit-slice
// reference, (2) 0 <= Count <= capacity, (3) conservation
// Count == sum(accepted Produce) - sum(accepted Consume), and (4) rejected
// operations leave no trace (the reference is only mutated on acceptance).
// It operates on fresh local queues and uses no shared state.
func (q *Queue) SelfCheck() bool {
	for _, cap := range []int64{0, -3} {
		if _, err := New(cap); !errors.Is(err, ErrInvalidCapacity) {
			return false
		}
	}
	seqs := []struct {
		cap int64
		ops []scOp
	}{
		// The NOTES.md eight-step scenario.
		{5, []scOp{
			{true, 3, true, nil}, {true, 2, true, nil}, {true, 1, false, nil},
			{false, 2, true, nil}, {true, 3, false, nil}, {false, 1, true, nil},
			{true, 3, true, nil}, {false, 6, false, ErrUnderflow},
		}},
		// Exact-fill boundary, invalid args, backpressure, underflow on empty.
		{3, []scOp{
			{true, 1, true, nil},
			{true, 0, false, ErrInvalidProduce},
			{false, 0, false, ErrInvalidConsume},
			{true, -5, false, ErrInvalidProduce},
			{false, -5, false, ErrInvalidConsume},
			{true, 2, true, nil}, // 1+2 == capacity: fill exactly, accepted
			{true, 1, false, nil},
			{false, 4, false, ErrUnderflow},
			{false, 3, true, nil}, // drain to empty
			{false, 1, false, ErrUnderflow},
			{true, 1, true, nil}, // still usable after all rejections
		}},
	}
	for _, seq := range seqs {
		qq, err := New(seq.cap)
		if err != nil {
			return false
		}
		var ref []struct{} // naive reference: append/pop element by element
		var sumP, sumC int64
		for _, op := range seq.ops {
			if op.prod {
				ok, err := qq.Produce(op.n)
				switch {
				case op.err != nil:
					if ok || !errors.Is(err, op.err) {
						return false
					}
				case op.accept:
					if !ok || err != nil {
						return false
					}
					for range op.n {
						ref = append(ref, struct{}{})
					}
					sumP += op.n
				default:
					if ok || err != nil {
						return false // expected clean backpressure
					}
				}
			} else {
				err := qq.Consume(op.n)
				if op.err != nil {
					if !errors.Is(err, op.err) {
						return false
					}
				} else {
					if err != nil || !op.accept {
						return false
					}
					ref = ref[op.n:]
					sumC += op.n
				}
			}
			cnt := qq.Count()
			if cnt != int64(len(ref)) || cnt < 0 || cnt > seq.cap || sumP-sumC != cnt {
				return false
			}
		}
	}
	return true
}
