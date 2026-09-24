// Package view orchestrates the foreground view and background rebuild.
// It depends only on dbuf.
package view

import (
	"sync"

	"ontology/dbuf"
)

// Event is one log entry.
type Event = dbuf.Event

// Sentinel errors, re-exported so callers see exactly four distinct causes.
var (
	ErrNotRebuilding     = dbuf.ErrNotRebuilding
	ErrAlreadyRebuilding = dbuf.ErrAlreadyRebuilding
	ErrRebuildIncomplete = dbuf.ErrRebuildIncomplete
	ErrInvalidEvent      = dbuf.ErrInvalidEvent
)

// Snapshot exposes the internal state for demos and diagnostics.
type Snapshot struct {
	F          map[string]int64
	B          map[string]int64
	Pending    []Event
	Rebuilding bool
}

// View is the materialized view: log L plus the double buffer.
type View struct {
	mu  sync.RWMutex
	buf *dbuf.Buffer
	log []Event // append-only event log L

	// bLookups counts hash probes into B made while double-writing during
	// a rebuild. Unexported on purpose: it must stay O(1) per Apply and is
	// never readable through any exported API.
	bLookups int
}

// New returns an empty View.
func New() *View { return &View{buf: dbuf.New()} }

func valid(ev Event) bool { return ev.Key != "" && ev.Delta != 0 }

// Apply validates the whole batch first; any invalid event rejects the
// entire batch with no state change. During a rebuild each event is also
// staged into the pending list (the double write).
func (v *View) Apply(evs ...Event) error {
	for _, ev := range evs {
		if !valid(ev) {
			return ErrInvalidEvent
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, ev := range evs {
		v.buf.ApplyFront(ev)
		v.log = append(v.log, ev)
		if v.buf.Rebuilding() {
			v.buf.Stage(ev)
			v.bLookups++ // one hash probe into B per staged event
		}
	}
	return nil
}

// StartRebuild opens the background buffer at the current log length.
func (v *View) StartRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.StartRebuild(len(v.log))
}

// RebuildStep replays one pre-snapshot history event into B.
func (v *View) RebuildStep(ev Event) error {
	if !valid(ev) {
		return ErrInvalidEvent
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.Replay(ev)
}

// CommitSwitch applies pending to B and atomically swaps F = B.
func (v *View) CommitSwitch() error { return v.finish(v.bufCommit()) }

func (v *View) bufCommit() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.Commit()
}

// AbortRebuild discards B and pending; F is untouched.
func (v *View) AbortRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.finishLocked(v.buf.Abort())
}

// finishLocked resets the probe counter when a rebuild ends successfully.
func (v *View) finishLocked(err error) error {
	if err == nil {
		v.bLookups = 0
	}
	return err
}

// View returns a copy of the foreground F. It never reads B.
func (v *View) View() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.buf.Front()
}

// Snapshot returns a consistent copy of F, B, pending and the flag.
func (v *View) Snapshot() Snapshot {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return Snapshot{v.buf.Front(), v.buf.Back(), v.buf.Pending(), v.buf.Rebuilding()}
}

// naive replays the whole log from scratch — the reference result.
func (v *View) naive() map[string]int64 {
	out := make(map[string]int64)
	for _, ev := range v.log {
		out[ev.Key] += ev.Delta
		if out[ev.Key] == 0 {
			delete(out, ev.Key)
		}
	}
	return out
}

// SelfCheck verifies internal invariants, including that the double-write
// B lookup count is exactly one hash probe per staged event (no scan of
// B). It reports only pass/fail; the counter itself stays unexported.
func (v *View) SelfCheck() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.buf.Rebuilding() && v.buf.Back() != nil {
		return false
	}
	if v.buf.Rebuilding() && v.buf.Stepped() > v.buf.Cursor() {
		return false
	}
	if v.bLookups != len(v.buf.Pending()) { // one probe per staged event
		return false
	}
	ref, front := v.naive(), v.buf.Front()
	if len(ref) != len(front) {
		return false
	}
	for k, n := range ref {
		if front[k] != n {
			return false
		}
	}
	return true
}
