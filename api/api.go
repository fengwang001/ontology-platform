// Package api is the public facade of the connID demultiplexer.
package api

import (
	"errors"
	"fmt"

	"ontology/demux"
)

// Handle identifies one opening of a connID slot.
type Handle = demux.Handle

// The four distinguishable, decidable sentinel errors.
var (
	ErrNoSlots  = demux.ErrNoSlots
	ErrBadID    = demux.ErrBadID
	ErrHalfOpen = demux.ErrHalfOpen
	ErrStale    = demux.ErrStale
)

// Demux multiplexes logical connections over one shared channel.
type Demux struct{ d *demux.Demux }

// New creates a demultiplexer with C slots, connID in [0, C).
func New(c int) *Demux { return &Demux{d: demux.New(c)} }

// Open allocates the smallest free connID; ErrNoSlots when none is free.
func (x *Demux) Open() (Handle, error) { return x.d.Open() }

// Close releases the slot; the handle must be OPEN with matching generation.
func (x *Demux) Close(h Handle) error { return x.d.Close(h) }

// Send validates the handle; this mechanism sends no frames.
func (x *Demux) Send(h Handle, data []byte) error { return x.d.Send(h, data) }

// Recv dispatches one inbound frame: ErrBadID / ErrHalfOpen / ErrStale, or
// FIFO-append to the connection's accumulated data.
func (x *Demux) Recv(id, gen int, data []byte) error { return x.d.Recv(id, gen, data) }

// Data returns the FIFO accumulation of connection id.
func (x *Demux) Data(id int) []byte { return x.d.Data(id) }

// Gen returns the current generation of slot id.
func (x *Demux) Gen(id int) int { return x.d.Gen(id) }

// naive is the literal step-by-step transcription of the stated rules,
// used by SelfCheck (and tests) as an independent oracle.
type naive struct {
	c    int
	open map[int]bool
	gen  map[int]int
	buf  map[int]string
}

func newNaive(c int) *naive {
	return &naive{c: c, open: map[int]bool{}, gen: map[int]int{}, buf: map[int]string{}}
}

func (r *naive) check(id, g int) error {
	switch {
	case id < 0 || id >= r.c:
		return ErrBadID
	case !r.open[id]:
		return ErrHalfOpen
	case g != r.gen[id]:
		return ErrStale
	}
	return nil
}

func (r *naive) open1() (Handle, error) {
	for i := 0; i < r.c; i++ {
		if !r.open[i] {
			r.gen[i]++
			r.open[i], r.buf[i] = true, ""
			return Handle{ID: i, Gen: r.gen[i]}, nil
		}
	}
	return Handle{}, ErrNoSlots
}

func (r *naive) close1(h Handle) error {
	if err := r.check(h.ID, h.Gen); err != nil {
		return err
	}
	r.open[h.ID], r.buf[h.ID] = false, ""
	return nil
}

func (r *naive) recv(id, g int, s string) error {
	if err := r.check(id, g); err != nil {
		return err
	}
	r.buf[id] += s
	return nil
}

// SelfCheck replays built-in operation sequences on fresh instances against
// the naive oracle, verifying all four invariants: naive equivalence,
// generation isolation, half-open detectability, no-trace-on-reject (every
// rejected op must match the oracle's error and leave state identical).
func (x *Demux) SelfCheck() error {
	for _, c := range []int{1, 4, 9} {
		d, r := New(c), newNaive(c)
		pool := []Handle{{ID: c, Gen: 1}} // entry 0 is always a bad id
		seed := uint32(42)
		rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
		for n := 0; n < 400; n++ {
			h := pool[rnd(len(pool))]
			s := string(rune('a' + n%26))
			var ei, ee error
			switch rnd(4) {
			case 0:
				hi, e1 := d.Open()
				hr, e2 := r.open1()
				ei, ee = e1, e2
				if ei == nil && hi != hr {
					return fmt.Errorf("selfcheck: handle %v vs %v", hi, hr)
				}
				if ei == nil {
					pool = append(pool, hi)
				}
			case 1:
				ei, ee = d.Close(h), r.close1(h)
			case 2:
				ei, ee = d.Send(h, nil), r.check(h.ID, h.Gen)
			default:
				ei, ee = d.Recv(h.ID, h.Gen, []byte(s)), r.recv(h.ID, h.Gen, s)
			}
			if ei != ee {
				return fmt.Errorf("selfcheck: c=%d n=%d: %v vs %v", c, n, ei, ee)
			}
			for i := 0; i < c; i++ {
				if d.Gen(i) != r.gen[i] || string(d.Data(i)) != r.buf[i] {
					return fmt.Errorf("selfcheck: state diverge at id %d", i)
				}
			}
		}
	}
	if !x.d.BoundOK() {
		return errors.New("selfcheck: open slot-inspection bound violated")
	}
	return nil
}
