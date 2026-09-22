// Package reasm reassembles out-of-order message fragments in memory.
//
// A Reassembler tracks many concurrent messages, enforces a global byte
// budget, and evicts incomplete messages once their time to live expires.
// All time decisions go through the injected clock; the zero value is not
// usable, construct with New. Reassembler is safe for concurrent use.
package reasm

import (
	"errors"
	"sync"
	"time"

	"ontology/budget"
	"ontology/frag"
)

// ErrTotalMismatch is returned when a fragment declares a total length that
// differs from the one previously seen for the same message ID.
var ErrTotalMismatch = errors.New("reasm: inconsistent total length for message")

// Status is a read-only snapshot of one in-flight message.
type Status struct {
	Received  int           // distinct bytes received so far
	Complete  bool          // whether [0, total) is fully covered
	Remaining time.Duration // time left before eviction
}

// message is the per-ID bookkeeping for an incomplete message.
type message struct {
	set      *frag.Set
	total    int
	deadline time.Time
}

// Reassembler reassembles fragments of many messages under a byte budget.
type Reassembler struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	ledger  *budget.Budget
	pending map[string]*message
}

// New returns a Reassembler that holds at most limit bytes of incomplete
// message data and evicts a message ttl after its first fragment arrives.
// now is the injected clock and must be safe to call under a lock.
func New(limit int64, ttl time.Duration, now func() time.Time) *Reassembler {
	return &Reassembler{
		now:     now,
		ttl:     ttl,
		ledger:  budget.New(limit),
		pending: make(map[string]*message),
	}
}

// Submit feeds one fragment of the message id into the reassembler.
//
// When the fragment completes the message, Submit returns the assembled
// bytes and true; exactly one caller observes this, after which the ID no
// longer occupies memory. Otherwise it returns (nil, false, nil).
func (r *Reassembler) Submit(id string, off int, data []byte, total int) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.evictLocked(now)

	m, ok := r.pending[id]
	if ok && m.total != total {
		return nil, false, ErrTotalMismatch
	}
	if !ok {
		set, err := frag.NewSet(total)
		if err != nil {
			return nil, false, err
		}
		m = &message{set: set, total: total, deadline: now.Add(r.ttl)}
	}
	if _, err := m.set.Add(off, data, func(n int) error {
		return r.ledger.TryAcquire(int64(n))
	}); err != nil {
		return nil, false, err
	}
	if !ok {
		r.pending[id] = m
	}
	if !m.set.Complete() {
		return nil, false, nil
	}
	delete(r.pending, id)
	r.ledger.Release(int64(m.set.Received()))
	return m.set.Bytes(), true, nil
}

// Status reports the current state of message id. Delivered or evicted
// messages report the zero Status.
func (r *Reassembler) Status(id string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(r.now())
	m, ok := r.pending[id]
	if !ok {
		return Status{}
	}
	remaining := max(m.deadline.Sub(r.now()), 0)
	return Status{
		Received:  m.set.Received(),
		Complete:  m.set.Complete(),
		Remaining: remaining,
	}
}

// Used reports the total bytes currently held by incomplete messages.
func (r *Reassembler) Used() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(r.now())
	return r.ledger.Used()
}

// evictLocked drops every message whose deadline has been reached. Expiry is
// left-closed: a message whose deadline equals now is evicted.
func (r *Reassembler) evictLocked(now time.Time) {
	for id, m := range r.pending {
		if !now.Before(m.deadline) {
			r.ledger.Release(int64(m.set.Received()))
			delete(r.pending, id)
		}
	}
}
