// Package reasm reassembles out-of-order message fragments, evicting
// incomplete messages by an injected clock and enforcing a global
// byte budget.
package reasm

import (
	"fmt"
	"sync"
	"time"

	"ontology/budget"
	"ontology/frag"
)

// ErrTotalMismatch is returned when a fragment declares a total
// length different from the one already recorded for its message ID.
var ErrTotalMismatch = fmt.Errorf("reasm: inconsistent total length for message")

// Re-exported sentinel errors so callers only import this package.
var (
	ErrEmptyFragment  = frag.ErrEmptyFragment
	ErrZeroTotal      = frag.ErrZeroTotal
	ErrOutOfRange     = frag.ErrOutOfRange
	ErrBudgetExceeded = budget.ErrExceeded
)

// ConflictError is re-exported from frag.
type ConflictError = frag.ConflictError

// Interval is re-exported from frag.
type Interval = frag.Interval

// Info is a read-only snapshot of one in-flight message.
type Info struct {
	Received  int
	Complete  bool
	Remaining time.Duration
}

type entry struct {
	set       *frag.Set
	expiresAt time.Time
}

// Reassembler manages many in-flight messages. It is safe for
// concurrent use.
type Reassembler struct {
	now    func() time.Time
	ttl    time.Duration
	ledger *budget.Budget

	mu   sync.Mutex
	msgs map[string]*entry
}

// New creates a Reassembler. now supplies the clock, ttl is the
// maximum lifetime of an incomplete message, and limit caps the total
// bytes held by all incomplete messages.
func New(now func() time.Time, ttl time.Duration, limit int) *Reassembler {
	return &Reassembler{
		now:    now,
		ttl:    ttl,
		ledger: budget.New(limit),
		msgs:   make(map[string]*entry),
	}
}

// Submit feeds one fragment for message id. When the fragment
// completes the message it returns the assembled bytes and
// complete=true exactly once; the message then frees all its memory.
func (r *Reassembler) Submit(id string, off int, data []byte, total int) (msg []byte, complete bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.evictExpired(r.now())

	e, ok := r.msgs[id]
	if !ok {
		set, err := frag.NewSet(total)
		if err != nil {
			return nil, false, err
		}
		e = &entry{set: set, expiresAt: r.now().Add(r.ttl)}
		r.msgs[id] = e
	} else if total != e.set.Total() {
		return nil, false, ErrTotalMismatch
	}

	added, err := e.set.Plan(off, data)
	if err != nil {
		return nil, false, err
	}
	if err := r.ledger.Reserve(added); err != nil {
		return nil, false, err
	}
	e.set.Apply(off, data)

	if e.set.Complete() {
		msg = e.set.Bytes()
		r.ledger.Release(e.set.Received())
		delete(r.msgs, id)
		return msg, true, nil
	}
	return nil, false, nil
}

// Query returns a snapshot of one in-flight message. Delivered or
// evicted messages report the zero Info.
func (r *Reassembler) Query(id string) Info {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.evictExpired(r.now())

	e, ok := r.msgs[id]
	if !ok {
		return Info{}
	}
	return Info{
		Received:  e.set.Received(),
		Complete:  e.set.Complete(),
		Remaining: e.expiresAt.Sub(r.now()),
	}
}

// Used returns the global bytes currently held by incomplete
// messages.
func (r *Reassembler) Used() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ledger.Used()
}

// Intervals returns the normalized (ascending, non-overlapping,
// non-adjacent) list of received byte ranges for id, or nil when the
// message is not in flight.
func (r *Reassembler) Intervals(id string) []Interval {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.evictExpired(r.now())

	e, ok := r.msgs[id]
	if !ok {
		return nil
	}
	return e.set.Intervals()
}

// evictExpired drops every message whose lifetime ended at or before
// now, releasing its budget. Caller must hold r.mu.
func (r *Reassembler) evictExpired(now time.Time) {
	for id, e := range r.msgs {
		if !now.Before(e.expiresAt) {
			r.ledger.Release(e.set.Received())
			delete(r.msgs, id)
		}
	}
}
