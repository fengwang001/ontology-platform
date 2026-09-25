// Package dispatch manages per-key reorder buffers and routes events by key.
// It depends only on the fifo package.
package dispatch

import (
	"errors"
	"sync"

	"ontology/fifo"
)

// Sentinel errors. The fifo errors are re-exported so callers of this
// package (and api) never need to import fifo just to classify errors.
var (
	ErrEmptyKey     = errors.New("dispatch: key must not be empty")
	ErrInvalidMax   = fifo.ErrInvalidMax
	ErrBackpressure = fifo.ErrBackpressure
)

// Dispatcher owns one fifo.KeyState per key, all sharing one in-flight bound.
type Dispatcher struct {
	mu   sync.Mutex
	max  int
	keys map[string]*fifo.KeyState
}

// New creates a dispatcher whose per-key buffers are bounded by maxInFlight.
func New(maxInFlight int) (*Dispatcher, error) {
	if maxInFlight <= 0 {
		return nil, ErrInvalidMax
	}
	return &Dispatcher{max: maxInFlight, keys: make(map[string]*fifo.KeyState)}, nil
}

// Feed routes (key, seq) to that key's buffer. It returns the seqs newly
// emitted for the key by this call.
func (d *Dispatcher) Feed(key string, seq int64) ([]int64, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	k, ok := d.keys[key]
	if !ok {
		k, _ = fifo.New(d.max) // d.max validated in New
		d.keys[key] = k
	}
	return k.Feed(seq)
}

// Emitted returns a copy of the key's emitted prefix; unknown key -> empty.
func (d *Dispatcher) Emitted(key string) []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if k := d.keys[key]; k != nil {
		return k.Emitted()
	}
	return []int64{}
}

// Buffered returns the key's current in-flight count; unknown key -> 0.
func (d *Dispatcher) Buffered(key string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if k := d.keys[key]; k != nil {
		return k.Buffered()
	}
	return 0
}

// Dropped returns the total number of duplicate drops across all keys.
func (d *Dispatcher) Dropped() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var n int64
	for _, k := range d.keys {
		n += k.Dropped()
	}
	return n
}

// SelfCheck verifies routing isolation, aggregate drop counting and the
// backpressure/no-trace property on built-in multi-key scenarios.
func (d *Dispatcher) SelfCheck() error {
	if err := fifo.SelfCheck(); err != nil {
		return err
	}
	e, err := New(3)
	if err != nil {
		return err
	}
	type feed struct {
		k string
		s int64
	}
	plan := []feed{
		{"K", 5}, {"K", 2}, {"K", 4}, {"K", 1}, {"K", 3},
		{"J", 3}, {"J", 1}, {"J", 2},
		{"K", 2}, // duplicate on K only; J must be unaffected
	}
	for _, f := range plan {
		if _, err := e.Feed(f.k, f.s); err != nil {
			return err
		}
	}
	if n := len(e.Emitted("K")); n != 5 {
		return errors.New("dispatch: K emitted count wrong")
	}
	if n := len(e.Emitted("J")); n != 3 {
		return errors.New("dispatch: J emitted count wrong")
	}
	if e.Dropped() != 1 {
		return errors.New("dispatch: aggregate dropped count wrong")
	}
	for _, s := range []int64{9, 10, 11} { // fill X's buffer to the bound
		if _, err := e.Feed("X", s); err != nil {
			return err
		}
	}
	if _, err := e.Feed("X", 12); !errors.Is(err, ErrBackpressure) {
		return errors.New("dispatch: backpressure misclassified")
	}
	if e.Buffered("X") != 3 || len(e.Emitted("X")) != 0 {
		return errors.New("dispatch: rejected feed left a trace")
	}
	return nil
}
