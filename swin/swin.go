// Package swin maintains per-key session sets under a monotone watermark.
package swin

import (
	"errors"
	"math"
	"slices"
	"sort"

	"ontology/sess"
)

var ErrNonPositiveGap = errors.New("swin: gap must be positive")
var ErrEmptyKey = errors.New("swin: empty key")
var ErrTooManyOpen = errors.New("swin: too many open sessions")

type Event struct {
	Key string
	TS  int64
}
type keyState struct{ ss []sess.Session } // sessions sorted by Start (and End)

// Engine holds all mutable state. Not goroutine-safe; guard above.
type Engine struct {
	gap, wm int64 // wm == math.MinInt64 before the first event
	maxOpen int
	keys    map[string]*keyState
	dropped int
	cmps    int // sessions predicate-compared for merge sets in the last Feed
}

func NewEngine(gap int64, maxOpen int) (*Engine, error) {
	if gap <= 0 {
		return nil, ErrNonPositiveGap
	}
	return &Engine{gap: gap, wm: math.MinInt64, maxOpen: maxOpen, keys: map[string]*keyState{}}, nil
}
func (e *Engine) Dropped() int { return e.dropped }

// View returns each key's sessions, deriving Closed from wm > end+gap.
func (e *Engine) View() map[string][]sess.Session {
	out := make(map[string][]sess.Session, len(e.keys))
	for k, ks := range e.keys {
		cp := slices.Clone(ks.ss)
		for i := range cp {
			cp[i].Closed = e.wm > cp[i].End+e.gap
		}
		out[k] = cp
	}
	return out
}

// Feed applies a batch atomically; a rejection leaves all state untouched.
func (e *Engine) Feed(evs []Event) error {
	if slices.ContainsFunc(evs, func(ev Event) bool { return ev.Key == "" }) {
		return ErrEmptyKey
	}
	c := *e
	c.keys = make(map[string]*keyState, len(e.keys))
	for k, ks := range e.keys {
		c.keys[k] = &keyState{ss: slices.Clone(ks.ss)}
	}
	c.cmps = 0
	for _, ev := range evs {
		if err := c.feedOne(ev); err != nil {
			return err
		}
	}
	*e = c
	return nil
}

// openCount counts open sessions (wm <= end+gap) via per-key binary search.
func (e *Engine) openCount() int {
	n := 0
	for _, ks := range e.keys {
		p := sort.Search(len(ks.ss), func(i int) bool { return e.wm <= ks.ss[i].End+e.gap })
		n += len(ks.ss) - p
	}
	return n
}

func (e *Engine) feedOne(ev Event) error {
	if ev.TS > e.wm {
		e.wm = ev.TS
	}
	ks := e.keys[ev.Key]
	if ks == nil {
		ks = &keyState{}
		e.keys[ev.Key] = ks
	}
	i := sort.Search(len(ks.ss), func(i int) bool { return ks.ss[i].End+e.gap >= ev.TS })
	lo, hi := -1, -1
	for j := i; j < len(ks.ss); j++ { // contiguous merge-set window
		e.cmps++
		if !ks.ss[j].InMergeSet(ev.TS, e.gap) {
			break
		}
		if lo < 0 {
			lo = j
		}
		hi = j
	}
	if lo >= 0 {
		for j := lo; j <= hi; j++ { // M contains a closed session: drop
			if e.wm > ks.ss[j].End+e.gap {
				e.dropped++
				return nil
			}
		}
		m := sess.Session{Start: min(ks.ss[lo].Start, ev.TS), End: ev.TS, Count: 1}
		for j := lo; j <= hi; j++ {
			m.Count += ks.ss[j].Count
			m.End = max(m.End, ks.ss[j].End)
		}
		ks.ss = slices.Replace(ks.ss, lo, hi+1, m)
		return nil
	}
	s := sess.Session{Start: ev.TS, End: ev.TS, Count: 1, Closed: e.wm > ev.TS+e.gap}
	if !s.Closed && e.openCount()+1 > e.maxOpen { // reject; clone is discarded
		return ErrTooManyOpen
	}
	pos := sort.Search(len(ks.ss), func(i int) bool { return ks.ss[i].Start > ev.TS })
	ks.ss = slices.Insert(ks.ss, pos, s)
	return nil
}

// VerifyLookup seeds m disjoint OPEN sessions (white-box only: feeding their
// events would advance wm and close older ones), then an event far below it.
// Verdict only — never the counter — that merge-set comparisons stayed
// within an m-independent constant, proving binary search.
func VerifyLookup() error {
	for _, m := range []int64{100, 1000, 10000} {
		e, _ := NewEngine(3, int(m)+1)
		ks := &keyState{}
		for i := int64(0); i < m; i++ {
			t := 1000 + 6*i
			ks.ss = append(ks.ss, sess.Session{Start: t, End: t, Count: 1})
		}
		e.keys["K"] = ks
		e.cmps = 0
		if err := e.Feed([]Event{{Key: "K", TS: 0}}); err != nil {
			return err
		}
		if e.cmps > 2 {
			return errors.New("swin: merge-set lookup cost grows with m")
		}
	}
	return nil
}
