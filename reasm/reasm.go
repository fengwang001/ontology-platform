// Package reasm reassembles out-of-order message fragments. It manages
// many in-flight messages, evicts expired ones using an injected clock,
// and enforces a global byte budget. All state lives in process memory.
package reasm

import (
	"sync"
	"time"

	"ontology/budget"
	"ontology/frag"
)

type entry struct {
	set      *frag.Set
	deadline time.Time
	stored   int64 // bytes charged to the budget
}

// Reassembler is safe for concurrent use.
type Reassembler struct {
	mu     sync.Mutex
	now    func() time.Time
	ttl    time.Duration
	budget *budget.Budget
	msgs   map[string]*entry
}

// New creates a Reassembler. now is the injected clock (the only time
// source used), ttl is how long an incomplete message may live, and
// limitBytes is the hard cap on bytes held by incomplete messages.
func New(now func() time.Time, ttl time.Duration, limitBytes int64) *Reassembler {
	return &Reassembler{
		now:    now,
		ttl:    ttl,
		budget: budget.New(limitBytes),
		msgs:   make(map[string]*entry),
	}
}

// Submit feeds one fragment for message id. When the fragment completes
// the message it returns (msg, true, nil) to exactly one caller and the
// message's memory is freed. Duplicate fragments are idempotent.
func (r *Reassembler) Submit(id string, off int, data []byte, total int) ([]byte, bool, error) {
	if len(data) == 0 {
		return nil, false, ErrEmptyData
	}
	if total == 0 {
		return nil, false, ErrZeroTotal
	}
	if off < 0 || total < 0 || off+len(data) > total {
		return nil, false, ErrOutOfRange
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()

	e, ok := r.msgs[id]
	if ok && e.set.Total() != total {
		return nil, false, ErrTotalMismatch
	}
	if !ok {
		if !r.budget.TryCharge(int64(len(data))) {
			return nil, false, ErrBudget
		}
		s := frag.New(total)
		s.Add(off, data)
		e = &entry{set: s, deadline: r.now().Add(r.ttl), stored: int64(len(data))}
		r.msgs[id] = e
	} else {
		dup, err := e.set.Check(off, data)
		if err != nil {
			return nil, false, err
		}
		if dup {
			return nil, false, nil
		}
		if !r.budget.TryCharge(int64(len(data))) {
			return nil, false, ErrBudget
		}
		e.set.Add(off, data)
		e.stored += int64(len(data))
	}

	if e.set.Complete() {
		msg := e.set.Assemble()
		r.budget.Release(e.stored)
		delete(r.msgs, id)
		return msg, true, nil
	}
	return nil, false, nil
}

// evictLocked drops every message whose deadline has been reached.
// Expiry is left-closed right-open: now == deadline means expired.
func (r *Reassembler) evictLocked() {
	now := r.now()
	for id, e := range r.msgs {
		if !now.Before(e.deadline) {
			r.budget.Release(e.stored)
			delete(r.msgs, id)
		}
	}
}
