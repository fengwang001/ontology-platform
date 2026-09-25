// Package demux dispatches frames carrying a connID to the right logical
// connection. It owns generation checks, half-open/stale verdicts, and
// per-connection FIFO buffering; slot allocation is delegated to slot.
package demux

import (
	"bytes"
	"errors"
	"sync"

	"ontology/slot"
)

// Handle re-exports the slot handle.
type Handle = slot.Handle

// The four distinguishable, decidable sentinel errors.
var (
	ErrNoSlots  = slot.ErrNoSlots
	ErrBadID    = slot.ErrBadID
	ErrHalfOpen = errors.New("demux: frame targets a FREE (half-open) conn id")
	ErrStale    = errors.New("demux: frame/handle generation does not match")
)

// Demux is safe for concurrent use.
type Demux struct {
	mu   sync.Mutex
	tab  *slot.Table
	data [][][]byte // per-slot accumulated chunks, FIFO
	gens []int      // current generation mirror, indexed by id
	open []bool     // occupancy mirror, indexed by id
}

// New creates a demux over c slots.
func New(c int) *Demux {
	return &Demux{
		tab:  slot.New(c),
		data: make([][][]byte, c),
		gens: make([]int, c),
		open: make([]bool, c),
	}
}

// Open allocates the smallest free connID and bumps its generation.
func (d *Demux) Open() (Handle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h, err := d.tab.Open()
	if err != nil {
		return Handle{}, err
	}
	d.open[h.ID] = true
	d.gens[h.ID] = h.Gen
	return h, nil
}

// validate applies all checks before any mutation: bad id, half-open, stale.
func (d *Demux) validate(h Handle) error {
	if h.ID < 0 || h.ID >= d.tab.C() {
		return ErrBadID
	}
	if !d.open[h.ID] {
		return ErrHalfOpen
	}
	if h.Gen != d.gens[h.ID] {
		return ErrStale
	}
	return nil
}

// Close requires an OPEN slot with matching generation, then frees it
// (generation retained) and drops the connection's accumulated data.
func (d *Demux) Close(h Handle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validate(h); err != nil {
		return err
	}
	d.tab.Free(h.ID)
	d.open[h.ID] = false
	d.data[h.ID] = nil
	return nil
}

// Send validates the handle only; this mechanism sends no frames, so no
// state is touched on success either.
func (d *Demux) Send(h Handle, payload []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.validate(h)
}

// Recv classifies one inbound frame and appends it FIFO only when the slot
// is OPEN and the generation matches. All error paths leave state untouched.
func (d *Demux) Recv(id, gen int, payload []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id < 0 || id >= d.tab.C() {
		return ErrBadID
	}
	if !d.open[id] {
		return ErrHalfOpen
	}
	if gen != d.gens[id] {
		return ErrStale
	}
	chunk := make([]byte, len(payload))
	copy(chunk, payload)
	d.data[id] = append(d.data[id], chunk)
	return nil
}

// Data returns the FIFO concatenation accumulated for id, nil for bad id.
func (d *Demux) Data(id int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id < 0 || id >= d.tab.C() {
		return nil
	}
	return bytes.Join(d.data[id], nil)
}

// Gen reports the current generation of id (0 if never opened), -1 if bad.
func (d *Demux) Gen(id int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id < 0 || id >= d.tab.C() {
		return -1
	}
	return d.gens[id]
}

// BoundOK reports whether every Open stayed within the logarithmic
// slot-inspection bound. It exposes a verdict, never a counter value.
func (d *Demux) BoundOK() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tab.BoundOK()
}
