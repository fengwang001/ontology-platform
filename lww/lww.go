// Package lww implements a single-replica LWW-Element-Set state CRDT.
package lww

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrElement   = errors.New("lww: empty element")
	ErrTimestamp = errors.New("lww: non-positive timestamp")
	ErrCapacity  = errors.New("lww: maxElems exceeded")
)

// Record holds one element's latest add/remove timestamps; 0 means absent.
type Record struct{ A, R int64 }

// Live: in the set iff added and A > R (tie biases to removal).
func (r Record) Live() bool { return r.A > 0 && r.A > r.R }

type entry struct {
	seq  uint64
	elem string
}

var nextID atomic.Uint64

// State is one replica; all methods are safe for concurrent use.
type State struct {
	id      uint64
	max     int
	mu      sync.Mutex
	recs    map[string]Record
	seq     uint64            // change counter, == len(log)
	log     []entry           // one entry per record change
	last    map[*State]uint64 // last merged seq per source
	checked int               // records examined by the latest Merge
}

func New(maxElems int) *State {
	return &State{id: nextID.Add(1), max: maxElems, recs: map[string]Record{}, last: map[*State]uint64{}}
}

// op validates, then applies max(cur, ts) to one side of e's record.
func (s *State) op(e string, ts int64, add bool) error {
	if e == "" {
		return ErrElement
	}
	if ts <= 0 {
		return ErrTimestamp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[e]
	if !ok && len(s.recs) >= s.max {
		return ErrCapacity
	}
	cur := rec.R
	if add {
		cur = rec.A
	}
	if ts <= cur {
		return nil // older timestamp: no change, no seq bump
	}
	if add {
		rec.A = ts
	} else {
		rec.R = ts
	}
	s.recs[e] = rec
	s.seq++
	s.log = append(s.log, entry{s.seq, e})
	return nil
}

func (s *State) Add(e string, ts int64) error    { return s.op(e, ts, true) }
func (s *State) Remove(e string, ts int64) error { return s.op(e, ts, false) }

func (s *State) Contains(e string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recs[e].Live()
}

// Snapshot returns a copy of all records.
func (s *State) Snapshot() map[string]Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Record, len(s.recs))
	for k, v := range s.recs {
		out[k] = v
	}
	return out
}

// Merge folds src into dst (dst only), processing only src's changes since
// dst's last merge with src; result is identical to a full-state merge.
func Merge(dst, src *State) error {
	if dst == src {
		return nil
	}
	a, b := dst, src // lock in id order to avoid deadlock
	if a.id > b.id {
		a, b = b, a
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	entries := src.log[dst.last[src]:]
	fresh := map[string]bool{}
	for _, en := range entries { // phase 1: capacity check, no writes
		if _, ok := dst.recs[en.elem]; !ok {
			fresh[en.elem] = true
		}
	}
	if len(dst.recs)+len(fresh) > dst.max {
		return ErrCapacity
	}
	for _, en := range entries { // phase 2: apply elementwise max
		sr, dr := src.recs[en.elem], dst.recs[en.elem]
		nr := dr
		if sr.A > nr.A {
			nr.A = sr.A
		}
		if sr.R > nr.R {
			nr.R = sr.R
		}
		if nr != dr {
			dst.recs[en.elem] = nr
			dst.seq++
			dst.log = append(dst.log, entry{dst.seq, en.elem})
		}
	}
	dst.last[src] = src.seq
	dst.checked = len(entries)
	return nil
}
