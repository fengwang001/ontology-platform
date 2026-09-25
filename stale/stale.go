// Package stale consumes a change stream (data, watermark, heartbeat) and
// decides whether a materialized view is stale. It depends only on wm.
package stale

import (
	"errors"
	"sync"

	"ontology/wm"
)

// Record types of the change stream.
type (
	// Data applies one event; Seq must be exactly applied+1.
	Data struct{ Seq, Val int64 }
	// Watermark declares that every Data with Seq <= UpTo should be visible.
	Watermark struct{ UpTo int64 }
	// Heartbeat is a liveness signal stamped at the current logical clock.
	Heartbeat struct{}
)

// ErrTickBacktrack: Tick was called with t below the current clock.
var ErrTickBacktrack = errors.New("stale: logical clock cannot move backwards")

// ErrUnknownRecord: Feed received a value that is no known record kind.
var ErrUnknownRecord = errors.New("stale: unknown record type")

// Rejections originating in wm are re-exported here so the layer above (api)
// depends on stale only; the dependency direction stays wm <- stale <- api.
var (
	ErrDataGap            = wm.ErrDataGap
	ErrWatermarkBacktrack = wm.ErrWatermarkBacktrack
)

// Detector is the stream consumer. All methods are safe for concurrent use;
// reads take an RLock so concurrent readers observe one consistent snapshot.
type Detector struct {
	mu sync.RWMutex
	s  wm.State

	now     int64 // logical clock, monotonic
	view    int64 // sum of Val over applied Data
	timeout int64 // fixed positive timeout constant

	// accessed counts history records consulted while handling the most
	// recent Feed/Tick. The decision uses only A/W/lastHB/now scalars, so
	// no history is ever stored or rescanned: it stays 0. Unexported on
	// purpose; no exported method exposes it.
	accessed int64
}

// NewDetector creates a detector with a fixed positive timeout constant.
// Callers must pass timeout > 0 (the api package enforces this publicly).
func NewDetector(timeout int64) *Detector {
	return &Detector{timeout: timeout}
}

// Tick advances the logical clock. t < now is rejected before any mutation.
func (d *Detector) Tick(t int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accessed = 0 // only the live scalar clock is consulted
	if t < d.now {
		return ErrTickBacktrack
	}
	d.now = t
	return nil
}

// Feed applies one record. A rejected record changes nothing: every
// validation below runs before the corresponding mutation.
func (d *Detector) Feed(rec any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accessed = 0 // no history records are ever consulted
	switch r := rec.(type) {
	case Data:
		// Apply mutates only after the contiguity check passes.
		if err := d.s.Apply(r.Seq); err != nil {
			return err
		}
		d.view += r.Val
	case Watermark:
		// Raise mutates only after the regression check passes.
		if err := d.s.Raise(r.UpTo); err != nil {
			return err
		}
	case Heartbeat:
		d.s.Beat(d.now) // now is monotonic, so lastHB cannot regress
	default:
		return ErrUnknownRecord
	}
	return nil
}

// Stale is exactly (A < W) || (now-lastHB > timeout): three O(1) compares.
func (d *Detector) Stale() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.s.Applied() < d.s.Mark() || d.now-d.s.LastBeat() > d.timeout
}

// Applied returns A, the last applied Data sequence.
func (d *Detector) Applied() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.s.Applied()
}

// Watermark returns W.
func (d *Detector) Watermark() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.s.Mark()
}

// LastBeat returns lastHB, the clock at the latest heartbeat.
func (d *Detector) LastBeat() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.s.LastBeat()
}

// Now returns the current logical clock.
func (d *Detector) Now() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.now
}

// View returns the sum of Val over every applied Data record.
func (d *Detector) View() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.view
}
