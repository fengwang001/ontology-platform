// Package api is the external facade; SelfCheck replays a built-in stream
// against an independent naive reference model.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/pool"
)

var ErrUnpinNotPinned = pool.ErrUnpinNotPinned
var ErrInvalidFrame = pool.ErrInvalidFrame
var ErrNoEvictableFrame = pool.ErrNoEvictableFrame

type API struct{ p *pool.Pool }

func New(numFrames int) *API               { return &API{p: pool.New(numFrames)} }
func (a *API) Pin(pageID int) (int, error) { return a.p.Pin(pageID) }
func (a *API) Unpin(fr int) error          { return a.p.Unpin(fr) }
func (a *API) MarkDirty(fr int) error      { return a.p.MarkDirty(fr) }
func (a *API) Writes() int                 { return a.p.Writes() }

// Independent naive model; PageID -1 means empty.
type naive struct {
	f  []pool.FrameSnapshot
	ix map[int]int
	w  int
}

func newNaive(n int) *naive {
	m := &naive{f: make([]pool.FrameSnapshot, n), ix: map[int]int{}}
	for i := range m.f {
		m.f[i].PageID = -1
	}
	return m
}

// pick returns the smallest empty frame, else the smallest pin-0 occupied frame.
func (m *naive) pick(empty bool) int {
	for i, x := range m.f {
		if (empty && x.PageID == -1) || (!empty && x.PageID != -1 && x.Pin == 0) {
			return i
		}
	}
	return -1
}

func (m *naive) pin(id int) (int, error) {
	if f, ok := m.ix[id]; ok {
		m.f[f].Pin++
		return f, nil
	}
	t := m.pick(true)
	if t < 0 {
		t = m.pick(false)
	}
	if t < 0 {
		return -1, ErrNoEvictableFrame
	}
	v := &m.f[t]
	if v.PageID != -1 {
		if v.Dirty {
			m.w++ // dirty victim written back first
		}
		delete(m.ix, v.PageID)
	}
	*v = pool.FrameSnapshot{PageID: id, Pin: 1}
	m.ix[id] = t
	return t, nil
}

// set handles 'U' (unpin) and 'D' (mark dirty) on the naive model.
func (m *naive) set(k byte, f int) error {
	if f < 0 || f >= len(m.f) {
		return ErrInvalidFrame
	}
	if k == 'U' {
		if m.f[f].Pin == 0 {
			return ErrUnpinNotPinned
		}
		m.f[f].Pin--
	} else {
		m.f[f].Dirty = true
	}
	return nil
}

type op struct {
	act byte // 'P'/'U'/'D'
	arg int
	err error
}

// match verifies frame equality and writes against the naive model; the model
// never duplicates a page (ix is page→one frame), so equality implies uniqueness.
func (a *API) match(m *naive) error {
	got := a.p.Snapshot()
	for i := range got {
		if got[i] != m.f[i] {
			return fmt.Errorf("frame %d diverges: %+v vs %+v", i, got[i], m.f[i])
		}
	}
	if a.Writes() != m.w {
		return fmt.Errorf("writes %d != %d", a.Writes(), m.w)
	}
	return nil
}

// SelfCheck replays the built-in stream and verifies all four invariants per op.
func (a *API) SelfCheck() error {
	s := []op{
		{'P', 1, nil}, {'P', 2, nil}, {'P', 3, nil}, {'D', 0, nil}, {'U', 0, nil},
		{'P', 4, nil}, {'U', 1, nil}, {'P', 5, nil}, // dirty flush, clean evict
		{'U', 0, nil}, {'U', 0, ErrUnpinNotPinned},
		{'U', 9, ErrInvalidFrame}, {'D', 9, ErrInvalidFrame},
		{'P', 4, nil}, {'P', 6, ErrNoEvictableFrame},
	}
	m := newNaive(len(a.p.Snapshot()))
	for _, o := range s {
		before, bw := a.p.Snapshot(), a.p.Writes()
		var rf, mf int
		var re, me error
		if o.act == 'P' {
			rf, re = a.Pin(o.arg)
			mf, me = m.pin(o.arg)
		} else if o.act == 'U' {
			re, me = a.Unpin(o.arg), m.set('U', o.arg)
		} else {
			re, me = a.MarkDirty(o.arg), m.set('D', o.arg)
		}
		if !errors.Is(re, o.err) || !errors.Is(me, o.err) {
			return fmt.Errorf("op %c(%d): err mismatch %v/%v", o.act, o.arg, re, me)
		}
		if o.err != nil { // rejected op must leave no trace
			if !reflect.DeepEqual(before, a.p.Snapshot()) || bw != a.Writes() {
				return fmt.Errorf("op %c(%d): rejected op mutated state", o.act, o.arg)
			}
			continue
		}
		if rf != mf {
			return fmt.Errorf("op %c(%d): frame %d != %d", o.act, o.arg, rf, mf)
		}
		if err := a.match(m); err != nil {
			return fmt.Errorf("op %c(%d): %w", o.act, o.arg, err)
		}
	}
	return nil
}
