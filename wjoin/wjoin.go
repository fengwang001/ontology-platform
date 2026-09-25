// Package wjoin implements the stateful symmetric window join on top of win.
package wjoin

import (
	"errors"
	"sort"
	"sync"

	"ontology/win"
)

var ErrBadWindow = errors.New("wjoin: window size W must be > 0")
var ErrBadDelay = errors.New("wjoin: delay must be >= 0")
var ErrEmptyKey = errors.New("wjoin: key must not be empty")
var ErrBadSide = errors.New("wjoin: side must be 'L' or 'R'")

// Event is one record of stream L or R; Side is 'L' or 'R'.
type Event struct {
	Key  string
	TS   int64
	Side byte
}

// Join is one emitted pair of a left and a right event.
type Join struct {
	Key                          string
	WindowStart, LeftTS, RightTS int64
}
type bucket struct{ l, r []int64 } // per-side TS, ascending
type bucketKey struct {
	key   string
	start int64
}
type endItem struct {
	end int64
	k   bucketKey
}

// Joiner is a symmetric tumbling-window stream joiner with watermark.
type Joiner struct {
	mu                         sync.Mutex
	w, delay                   int64
	wm                         int64
	seen                       bool // false: no event seen yet, wm is "minus infinity"
	buckets                    map[bucketKey]*bucket
	ends                       []endItem // sorted by window end, for ordered cleanup
	joins                      []Join
	dropped, retained, checked int // checked: buckets examined in last cleanup
}

func New(w, delay int64) (*Joiner, error) {
	switch {
	case w <= 0:
		return nil, ErrBadWindow
	case delay < 0:
		return nil, ErrBadDelay
	}
	return &Joiner{w: w, delay: delay, buckets: map[bucketKey]*bucket{}}, nil
}

// Feed validates the whole batch first: any rejection fails atomically, leaving no trace.
func (j *Joiner) Feed(evs []Event) ([]Join, error) {
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		if e.Side != 'L' && e.Side != 'R' {
			return nil, ErrBadSide
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []Join
	for _, e := range evs {
		out = append(out, j.feedOne(e)...)
	}
	j.joins = append(j.joins, out...)
	return out, nil
}

func (j *Joiner) feedOne(e Event) []Join {
	j.advance(e.TS - j.delay) // step 1: wm only moves forward
	w := win.Of(e.TS, j.w)
	if w.Late(j.wm) { // step 2: drop late events
		j.dropped++
		j.cleanup()
		return nil
	}
	k := bucketKey{e.Key, w.Start}
	b := j.buckets[k]
	if b == nil {
		b = &bucket{}
		j.buckets[k] = b
		j.ends = insert(j.ends, endItem{w.End, k}, func(x, y endItem) bool { return x.end <= y.end })
	}
	other, mine := &b.l, &b.r
	if e.Side == 'R' {
		other, mine = &b.r, &b.l
	}
	var out []Join // step 3: pair with retained other-side events, TS ascending
	for _, ts := range *other {
		jn := Join{Key: e.Key, WindowStart: w.Start, LeftTS: ts, RightTS: e.TS}
		if e.Side == 'L' {
			jn.LeftTS, jn.RightTS = e.TS, ts
		}
		out = append(out, jn)
	}
	*mine = insert(*mine, e.TS, func(x, y int64) bool { return x < y })
	j.retained++
	j.cleanup() // step 4: clear every bucket whose window end <= wm
	return out
}

// advance moves the watermark forward only; it never regresses.
func (j *Joiner) advance(nwm int64) {
	if !j.seen || nwm > j.wm {
		j.wm, j.seen = nwm, true
	}
}
func insert[S ~[]E, E any](s S, x E, before func(x, e E) bool) S {
	i := sort.Search(len(s), func(i int) bool { return before(x, s[i]) })
	s = append(s, x)
	copy(s[i+1:], s[i:])
	s[i] = x
	return s
}

// cleanup clears buckets in window-end order; it never scans the whole table.
func (j *Joiner) cleanup() {
	j.checked = 0
	for len(j.ends) > 0 && j.ends[0].end <= j.wm {
		b := j.buckets[j.ends[0].k]
		j.retained -= len(b.l) + len(b.r)
		delete(j.buckets, j.ends[0].k)
		j.ends = j.ends[1:]
		j.checked++
	}
}

// Flush pushes the watermark to +infinity, closing every window.
func (j *Joiner) Flush() { j.mu.Lock(); defer j.mu.Unlock(); j.wm, j.seen = 1<<62, true; j.cleanup() }

// Joins returns a copy of every join emitted so far.
func (j *Joiner) Joins() []Join {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Join(nil), j.joins...)
}
func (j *Joiner) Retained() int { j.mu.Lock(); defer j.mu.Unlock(); return j.retained }
func (j *Joiner) Dropped() int  { j.mu.Lock(); defer j.mu.Unlock(); return j.dropped }
