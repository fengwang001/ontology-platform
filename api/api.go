// Package api is the external facade over the in-process event bus.
package api

import (
	"errors"
	"fmt"

	"ontology/bus"
)

// The three failure classes are distinct sentinels, errors.Is-able.
var (
	ErrInvalidN          = bus.ErrInvalidN
	ErrInvalidC          = bus.ErrInvalidCapacity
	ErrInvalidSubscriber = bus.ErrOutOfRange
)

// API is an in-process CDC event bus with N independent bounded queues.
type API struct {
	b *bus.Bus
	n int
}

// New builds n subscribers, each with a capacity-c queue; a rejected
// call constructs nothing and leaves no state behind.
func New(n, c int) (*API, error) {
	bb, err := bus.New(n, c)
	if err != nil {
		return nil, err
	}
	return &API{b: bb, n: n}, nil
}

// checkSI panics with the distinct sentinel before touching any state;
// recover and errors.Is the value (signatures carry no error return).
func (a *API) checkSI(si int) {
	if si < 0 || si >= a.n {
		panic(ErrInvalidSubscriber)
	}
}

func (a *API) Publish(ev int64) { a.b.Publish(ev) }

func (a *API) Consume(si int) (int64, bool) {
	a.checkSI(si)
	ev, ok, _ := a.b.Consume(si)
	return ev, ok
}

func (a *API) DropCount(si int) int { a.checkSI(si); d, _ := a.b.DropCount(si); return d }

func (a *API) QueueLen(si int) int { a.checkSI(si); l, _ := a.b.QueueLen(si); return l }

// drainAll empties every queue through Consume and returns contents.
func drainAll(a *API, n int) [][]int64 {
	out := make([][]int64, n)
	for si := range out {
		for ev, ok := a.Consume(si); ok; ev, ok = a.Consume(si) {
			out[si] = append(out[si], ev)
		}
	}
	return out
}

func guard(f func()) (r any) {
	defer func() { r = recover() }()
	f()
	return
}

// op is one sequence step: pub publishes v; otherwise consume from si.
type op struct {
	pub bool
	v   int64
	si  int
}

// SelfCheck verifies the invariants against a naive single-step reference.
func (a *API) SelfCheck() error {
	const n, c = 2, 2
	g, err := New(n, c)
	if err != nil {
		return err
	}
	ref := make([][]int64, n) // naive FIFO queues
	drops := make([]int, n)
	// First six ops are the NOTES.md six-step table; rest add drops/drains.
	ops := []op{
		{true, 1, 0}, {true, 2, 0}, {true, 3, 0}, {false, 0, 0},
		{true, 4, 0}, {false, 0, 1}, {false, 0, 0},
		{true, 5, 0}, {true, 6, 0}, {false, 0, 1}, {false, 0, 1},
		{false, 0, 1}, // ends: s0=[4,5], s1=[]
	}
	for i, o := range ops {
		if o.pub {
			g.Publish(o.v)
			for si := range ref { // naive: enqueue if not full, else tail-drop
				if len(ref[si]) < c {
					ref[si] = append(ref[si], o.v)
				} else {
					drops[si]++
				}
			}
		} else {
			rv, rok := int64(0), false // naive head pop
			if len(ref[o.si]) > 0 {
				rv, rok, ref[o.si] = ref[o.si][0], true, ref[o.si][1:]
			}
			if ev, ok := g.Consume(o.si); ev != rv || ok != rok {
				return fmt.Errorf("op %d consume got (%v,%v) want (%v,%v)", i, ev, ok, rv, rok)
			}
		}
		for si := 0; si < n; si++ { // len + drops pin queue at each step
			gl, gd := g.QueueLen(si), g.DropCount(si)
			if gl != len(ref[si]) || gd != drops[si] {
				return fmt.Errorf("op %d s%d: len %d/%d drop %d/%d", i, si, gl, len(ref[si]), gd, drops[si])
			}
		}
	}
	if got := drainAll(g, n); fmt.Sprint(got) != fmt.Sprint(ref) {
		return fmt.Errorf("final drain got %v want %v", got, ref)
	}

	// Rejected ops: three distinct decidable errors, no state change.
	if _, e := New(0, 2); !errors.Is(e, ErrInvalidN) {
		return fmt.Errorf("N<=0 got %v", e)
	}
	if _, e := New(2, 0); !errors.Is(e, ErrInvalidC) || errors.Is(e, ErrInvalidN) {
		return fmt.Errorf("C<=0 got %v", e)
	}
	g2, _ := New(2, 2)
	g2.Publish(7)
	bad := []func(){func() { g2.Consume(-1) }, func() { _ = g2.DropCount(2) }, func() { _ = g2.QueueLen(9) }}
	for i, f := range bad {
		x, ok := guard(f).(error)
		if !ok || !errors.Is(x, ErrInvalidSubscriber) {
			return fmt.Errorf("bad call %d recovered %v", i, x)
		}
	}
	if v, ok := g2.Consume(0); v != 7 || !ok { // still usable, untouched
		return fmt.Errorf("state changed after rejected calls: (%v,%v)", v, ok)
	}
	return nil
}
