// Package api is the public bounded out-of-orderness reorder buffer.
// The main output is strictly ordered by event time; too-late events go
// to a side output and are never dropped. Depends on rbuf.
package api

import (
	"errors"
	"math"
	"sync"

	"ontology/order"
	"ontology/rbuf"
)

// Out is one emitted event.
type Out struct {
	ID  string
	TS  int64
	Seq int64
}

// Distinct, decidable sentinel errors.
var (
	ErrInvalidParam = errors.New("api: invalid parameter")
	ErrEmptyID      = errors.New("api: empty id")
	ErrDuplicateID  = errors.New("api: duplicate id")
	ErrFull         = errors.New("api: buffer full")
)

// Reorder is safe for concurrent use.
type Reorder struct {
	mu    sync.Mutex
	delay int64
	buf   *rbuf.Buffer
	seen  map[string]struct{}
	wm    int64
	hasWM bool
	seq   int64
	main  []Out
	side  []Out
}

// New creates a buffer with the given allowed lateness and capacity.
func New(delay int64, maxBuffered int) (*Reorder, error) {
	if delay < 0 || maxBuffered <= 0 {
		return nil, ErrInvalidParam
	}
	return &Reorder{delay: delay, buf: rbuf.New(maxBuffered), seen: map[string]struct{}{}}, nil
}

// Push ingests one event and returns the main and side outputs produced by
// this call alone. A rejected call leaves every piece of state untouched.
func (r *Reorder) Push(id string, ts int64) (main, side []Out, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" {
		return nil, nil, ErrEmptyID
	}
	if _, dup := r.seen[id]; dup {
		return nil, nil, ErrDuplicateID
	}
	seq := r.seq // tentative: consumed only when the event is accepted
	if order.Late(ts, r.wm, r.hasWM) {
		r.commit(id, seq, ts)
		o := Out{id, ts, seq}
		r.side = append(r.side, o)
		return nil, []Out{o}, nil
	}
	if e := r.buf.Add(rbuf.Event{ID: id, TS: ts, Seq: seq}); e != nil {
		return nil, nil, ErrFull // Add fails atomically: no trace left
	}
	r.commit(id, seq, ts)
	main = convert(r.buf.Release(r.wm))
	r.main = append(r.main, main...)
	return main, nil, nil
}

// commit performs the bookkeeping shared by every accepted event: claim the
// ID, consume the arrival sequence number, advance the watermark (step 2).
func (r *Reorder) commit(id string, seq, ts int64) {
	r.seen[id] = struct{}{}
	r.seq = seq + 1
	r.wm, r.hasWM = order.Advance(r.wm, r.hasWM, ts, r.delay)
}

// Flush advances the watermark to plus infinity and releases everything.
// Events arriving afterwards are all late.
func (r *Reorder) Flush() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hasWM, r.wm = true, math.MaxInt64
	out := convert(r.buf.Release(r.wm))
	r.main = append(r.main, out...)
	return out
}

// Main returns all main-output events produced so far.
func (r *Reorder) Main() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Out(nil), r.main...)
}

// Side returns all side-output events produced so far.
func (r *Reorder) Side() []Out {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Out(nil), r.side...)
}

func convert(evs []rbuf.Event) []Out {
	out := make([]Out, len(evs))
	for i, e := range evs {
		out[i] = Out{e.ID, e.TS, e.Seq}
	}
	return out
}
