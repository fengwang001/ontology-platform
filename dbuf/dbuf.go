// Package dbuf holds the double-buffer state for a materialized view:
// foreground F, background B, the pending list and the rebuild flag.
// It has no dependencies on other packages of this module.
package dbuf

import "errors"

// Sentinel errors; every rejected operation maps to exactly one of them.
var (
	ErrNotRebuilding     = errors.New("dbuf: not rebuilding")
	ErrAlreadyRebuilding = errors.New("dbuf: already rebuilding")
	ErrRebuildIncomplete = errors.New("dbuf: rebuild incomplete, cursor not reached")
	ErrRebuildStepOOB    = errors.New("dbuf: rebuild step out of snapshot bounds")
	ErrInvalidEvent      = errors.New("dbuf: invalid event (empty key or zero delta)")
)

// Event is one log entry: count[Key] += Delta.
type Event struct {
	Key   string
	Delta int64
}

// Buffer is the double-buffer state. The zero value is ready to use.
type Buffer struct {
	f       map[string]int64 // foreground, always serving
	b       map[string]int64 // background, nil unless rebuilding
	pend    []Event          // events staged during rebuild, applied at commit
	reb     bool             // rebuild in progress
	cursor  int              // snapshot point: L length at StartRebuild
	stepped int              // history events replayed into B so far
}

// apply folds one event into m; a zero count deletes the key.
func apply(m map[string]int64, ev Event) {
	m[ev.Key] += ev.Delta
	if m[ev.Key] == 0 {
		delete(m, ev.Key)
	}
}

// ApplyFront applies a fresh event to F.
func (d *Buffer) ApplyFront(ev Event) { apply(d.f, ev) }

// StartRebuild opens B and records the snapshot point. F keeps serving.
func (d *Buffer) StartRebuild(cursor int) error {
	if d.reb {
		return ErrAlreadyRebuilding
	}
	d.b = make(map[string]int64)
	d.pend = nil
	d.reb = true
	d.cursor = cursor
	d.stepped = 0
	return nil
}

// Replay folds one pre-snapshot history event into B.
func (d *Buffer) Replay(ev Event) error {
	if !d.reb {
		return ErrNotRebuilding
	}
	if d.stepped >= d.cursor {
		return ErrRebuildStepOOB
	}
	apply(d.b, ev)
	d.stepped++
	return nil
}

// Stage records a fresh event in the pending list. It returns true when
// the key is currently absent from B — one hash probe, O(1) regardless of
// len(B). The caller may count this probe as the double-write B lookup.
func (d *Buffer) Stage(ev Event) (absentInB bool) {
	_, ok := d.b[ev.Key]
	d.pend = append(d.pend, ev)
	return !ok
}

// Commit replays pending into B, then atomically swaps F = B.
func (d *Buffer) Commit() error {
	if !d.reb {
		return ErrNotRebuilding
	}
	if d.stepped != d.cursor {
		return ErrRebuildIncomplete
	}
	for _, ev := range d.pend {
		apply(d.b, ev)
	}
	d.f, d.b = d.b, nil
	d.pend = nil
	d.reb = false
	return nil
}

// Abort discards B and pending; F is untouched.
func (d *Buffer) Abort() error {
	if !d.reb {
		return ErrNotRebuilding
	}
	d.b = nil
	d.pend = nil
	d.reb = false
	return nil
}

// Front returns a copy of F.
func (d *Buffer) Front() map[string]int64 {
	out := make(map[string]int64, len(d.f))
	for k, v := range d.f {
		out[k] = v
	}
	return out
}

// Back returns a copy of B (nil when not rebuilding).
func (d *Buffer) Back() map[string]int64 {
	if d.b == nil {
		return nil
	}
	out := make(map[string]int64, len(d.b))
	for k, v := range d.b {
		out[k] = v
	}
	return out
}

// Pending returns a copy of the staged events.
func (d *Buffer) Pending() []Event { return append([]Event(nil), d.pend...) }

// Rebuilding reports whether a rebuild is in progress.
func (d *Buffer) Rebuilding() bool { return d.reb }

// Cursor is the snapshot point recorded at StartRebuild.
func (d *Buffer) Cursor() int { return d.cursor }

// Stepped is the number of history events replayed into B.
func (d *Buffer) Stepped() int { return d.stepped }

// New returns an empty Buffer.
func New() *Buffer { return &Buffer{f: make(map[string]int64)} }
