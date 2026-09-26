// Package api is the external entry point of the bounded queue with
// backpressure. It depends only on bp (which in turn depends on bq).
package api

import (
	"errors"
	"fmt"

	"ontology/bp"
)

// Queue is the externally visible bounded FIFO queue.
type Queue struct {
	c   *bp.Controller
	cap int64
}

// New builds a Queue. capacity must be >= 1, otherwise bp.ErrBadCapacity.
func New(capacity int64) (*Queue, error) {
	c, err := bp.NewController(capacity)
	if err != nil {
		return nil, err
	}
	return &Queue{c: c, cap: capacity}, nil
}

// Produce places n elements all-or-none. A full queue returns (false, nil):
// backpressure, the caller retries later and nothing was placed.
func (q *Queue) Produce(n int64) (bool, error) { return q.c.Produce(n) }

// Consume removes n elements all-or-none. It rejects n <= 0 and underflow.
func (q *Queue) Consume(n int64) error { return q.c.Consume(n) }

// Count returns the current number of elements.
func (q *Queue) Count() int64 { return q.c.Count() }

// Free returns the remaining capacity.
func (q *Queue) Free() int64 { return q.c.Free() }

type step struct {
	produce bool
	n       int64
}

// SelfCheck replays built-in programs and verifies all four invariants:
// (1) agreement with a naive per-element slice, (2) capacity bounds,
// (3) conservation sum(produced)-sum(consumed), (4) rejected ops leave no
// trace. It returns nil iff every check holds.
func (q *Queue) SelfCheck() error {
	if _, err := New(0); !errors.Is(err, bp.ErrBadCapacity) {
		return fmt.Errorf("SelfCheck: capacity<=0 not rejected: %v", err)
	}
	if err := replay(5, canonicalSteps()); err != nil {
		return err
	}
	return replay(7, mixedSteps(7, 150))
}

// canonicalSteps is the eight-step program: capacity 5.
func canonicalSteps() []step {
	return []step{
		{true, 3}, {true, 2}, {true, 1}, {false, 2},
		{true, 3}, {false, 1}, {true, 3}, {false, 6},
	}
}

// mixedSteps deterministically generates a varied program (a linear
// congruential sequence) that includes exact fills, backpressure, illegal n
// and underflow.
func mixedSteps(seed int64, k int) []step {
	out := make([]step, k)
	x := seed
	for i := 0; i < k; i++ {
		x = (x*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		produce := x&1 == 0
		n := int64(x%5) - 1 // -1..3: includes 0/-1 (illegal) and overshoots
		out[i] = step{produce, n}
	}
	return out
}

// replay runs ops against a fresh queue and an independent naive model.
func replay(capacity int64, ops []step) error {
	q, err := New(capacity)
	if err != nil {
		return err
	}
	// ref is the naive reference: an explicit slice appended/popped one
	// element at a time. sumP/sumC track accepted amounts for conservation.
	var ref []int64
	var sumP, sumC int64
	for i, o := range ops {
		if o.produce {
			accept := o.n >= 1 && int64(len(ref))+o.n <= capacity
			ok, perr := q.Produce(o.n)
			switch {
			case accept && (!ok || perr != nil):
				return fmt.Errorf("step %d Produce(%d): want accept", i, o.n)
			case !accept && o.n <= 0 && !errors.Is(perr, bp.ErrIllegalProduce):
				return fmt.Errorf("step %d Produce(%d): want illegal-produce err", i, o.n)
			case !accept && o.n >= 1 && (ok || perr != nil):
				return fmt.Errorf("step %d Produce(%d): want backpressure", i, o.n)
			case accept:
				for k := int64(0); k < o.n; k++ {
					ref = append(ref, 1)
				}
				sumP += o.n
			}
		} else {
			accept := o.n >= 1 && o.n <= int64(len(ref))
			cerr := q.Consume(o.n)
			switch {
			case accept && cerr != nil:
				return fmt.Errorf("step %d Consume(%d): want accept", i, o.n)
			case !accept && o.n <= 0 && !errors.Is(cerr, bp.ErrIllegalConsume):
				return fmt.Errorf("step %d Consume(%d): want illegal-consume err", i, o.n)
			case !accept && o.n >= 1 && !errors.Is(cerr, bp.ErrUnderflow):
				return fmt.Errorf("step %d Consume(%d): want underflow err", i, o.n)
			case accept:
				ref = ref[o.n:]
				sumC += o.n
			}
		}
		cnt := q.Count()
		// Invariant 4 (no trace on rejection): ref/sumP/sumC were not
		// advanced on a rejected op, so equality below also proves the real
		// count did not move on that op.
		if cnt != int64(len(ref)) { // invariant 1: naive-reference agreement
			return fmt.Errorf("step %d: count=%d naive=%d", i, cnt, len(ref))
		}
		if cnt < 0 || cnt > capacity { // invariant 2: capacity bounds
			return fmt.Errorf("step %d: count %d out of [0,%d]", i, cnt, capacity)
		}
		if cnt != sumP-sumC { // invariant 3: conservation
			return fmt.Errorf("step %d: count=%d != %d-%d", i, cnt, sumP, sumC)
		}
	}
	return nil
}
