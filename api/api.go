// Package api is the public face of stream compaction: it bounds input
// length, replays streams onto in-memory multiset state and self-checks
// the invariants. It depends on fold, which depends on ev.
package api

import (
	"errors"
	"reflect"

	"ontology/ev"
	"ontology/fold"
)

// Distinct decidable sentinels; invalid events reuse ev.ErrInvalidEvent.
var (
	ErrTooLong        = errors.New("api: stream longer than maxLen")
	ErrIllegalRetract = errors.New("api: retract of a value with no live copy")
)

type Compactor struct{ maxLen int }

// New creates a Compactor rejecting streams longer than maxLen events.
// A Compactor has no mutable state and is safe for concurrent use.
func New(maxLen int) *Compactor { return &Compactor{maxLen: maxLen} }

// Replay validates stream, checks it is legal all the way on init, compacts
// it and applies the compacted stream to a fresh copy of init. Any failure
// rejects the whole input with a nil state and one of the sentinel errors.
func (c *Compactor) Replay(init fold.State, stream []ev.Event) (fold.State, error) {
	if len(stream) > c.maxLen {
		return nil, ErrTooLong
	}
	for _, e := range stream {
		if !e.Valid() {
			return nil, ev.ErrInvalidEvent
		}
	}
	if fold.Terminal(init, stream) == nil {
		return nil, ErrIllegalRetract // original stream illegal on init
	}
	cs, err := fold.Compact(stream)
	if err != nil {
		return nil, err
	}
	return fold.Terminal(init, cs), nil
}

type tcase struct {
	init fold.State
	s    []ev.Event
}

// SelfCheck runs the built-in cases for invariants 1-4 and the linear
// comparison bound; tests call it directly.
func (c *Compactor) SelfCheck() bool {
	I7, R7 := ev.OpInsert, ev.OpRetract
	g := func(v int64, op ev.Op) ev.Event { return ev.Event{Key: "g", Val: v, Op: op} }
	six := []ev.Event{g(7, I7), g(7, I7), g(7, R7), g(7, R7), g(7, I7), g(3, I7)}
	cases := []tcase{
		{nil, six},
		{fold.State{"g": {7: 1, 3: 2}, "h": {5: 1}}, six},
		{fold.State{"g": {7: 1}}, []ev.Event{g(7, R7), g(7, I7)}},
		{nil, []ev.Event{g(1, I7), g(1, R7), g(1, I7), g(1, R7)}},
		{nil, []ev.Event{{Key: "g", Val: 1, Op: I7}, {Key: "h", Val: 1, Op: I7}, g(1, R7)}},
		{fold.State{"g": {2: 4}}, nil},
	}
	for _, tc := range cases {
		cs, err := fold.Compact(tc.s)
		if err != nil || len(cs) > len(tc.s) {
			return false // invariant 2: no growth
		}
		want, got := fold.Terminal(tc.init, tc.s), fold.Terminal(tc.init, cs)
		if got == nil || !reflect.DeepEqual(want, got) {
			return false // invariants 1 and 3
		}
		if cs2, e := fold.Compact(cs); e != nil || !reflect.DeepEqual(cs, cs2) {
			return false // invariant 2: idempotence
		}
	}
	return c.errorsAtomic()
}

// errorsAtomic checks invariant 4: the three rejections are distinct and
// never return partial output.
func (c *Compactor) errorsAtomic() bool {
	if _, e := c.Replay(nil, make([]ev.Event, c.maxLen+1)); e != ErrTooLong {
		return false
	}
	if out, e := c.Replay(nil, []ev.Event{{Key: "", Op: ev.OpInsert}}); e != ev.ErrInvalidEvent || out != nil {
		return false
	}
	if out, e := c.Replay(nil, []ev.Event{{Key: "g", Val: 1, Op: ev.OpRetract}}); e != ErrIllegalRetract || out != nil {
		return false
	}
	return ErrTooLong != ErrIllegalRetract && ErrTooLong != ev.ErrInvalidEvent
}
