// Package register implements a first-write-wins deduplication register.
package register

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Write is a single candidate write addressed by key and logical sequence.
type Write struct {
	Key   string
	Seq   int64
	Value string
}

// ChangeKind discriminates the two kinds of materialized changes.
type ChangeKind string

const (
	// ChangeEstablish records a new effective value for a key.
	ChangeEstablish ChangeKind = "establish"
	// ChangeWithdraw records the removal of the previously effective write.
	ChangeWithdraw ChangeKind = "withdraw"
)

// Change is one entry in the append-only change log.
type Change struct {
	Kind  ChangeKind
	Key   string
	Seq   int64
	Value string
}

// Entry is the effective materialized write for one key.
type Entry struct {
	Seq   int64
	Value string
}

// Distinguishable rejection reasons. Use errors.Is to match them.
var (
	// ErrEmptyKey is reported when a write carries an empty key.
	ErrEmptyKey = errors.New("register: empty key")
	// ErrInvalidSeq is reported when a write carries a non-positive sequence.
	ErrInvalidSeq = errors.New("register: sequence must be positive")
	// ErrDuplicateSeq is reported when a write repeats the sequence of the
	// effective write of the same key, either inside one batch or against
	// the materialized state.
	ErrDuplicateSeq = errors.New("register: duplicate sequence for key")
)

// RejectError identifies the position and reason of a rejected write.
type RejectError struct {
	Index int
	Write Write
	Err   error
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("register: batch rejected at index %d (key=%q seq=%d): %v",
		e.Index, e.Write.Key, e.Write.Seq, e.Err)
}

func (e *RejectError) Unwrap() error { return e.Err }

// Register is the first-write-wins deduplication register.
type Register struct {
	mu        sync.RWMutex
	entries   map[string]Entry
	log       []Change
	discarded int64
}

// New returns an empty register.
func New() *Register { return &Register{entries: map[string]Entry{}} }

// Apply validates and applies a batch of writes atomically.
//
// Writes are applied in order. For each write: with no effective value the
// write establishes one; with a smaller sequence the current effective write
// is withdrawn first and the new one established; with a larger sequence the
// write loses as a late write and is counted as discarded. A withdraw change
// always matches the write materialized at that moment, key, sequence and
// value included.
//
// If any write is invalid the whole batch is rejected: no state changes, no
// log entries are appended and the discarded counter is untouched.
func (r *Register) Apply(writes []Write) (changes []Change, discarded int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if reject := r.validateLocked(writes); reject != nil {
		return nil, 0, reject
	}

	changes = make([]Change, 0, len(writes)*2)
	for _, w := range writes {
		current, ok := r.entries[w.Key]
		switch {
		case !ok || w.Seq < current.Seq:
			if ok {
				changes = append(changes, Change{
					Kind: ChangeWithdraw, Key: w.Key, Seq: current.Seq, Value: current.Value,
				})
			}
			r.entries[w.Key] = Entry{Seq: w.Seq, Value: w.Value}
			changes = append(changes, Change{
				Kind: ChangeEstablish, Key: w.Key, Seq: w.Seq, Value: w.Value,
			})
		case w.Seq > current.Seq:
			discarded++
			r.discarded++
		}
		// Equal sequences cannot occur here: validate rejects them.
	}
	r.log = append(r.log, changes...)
	return changes, discarded, nil
}

// validateLocked checks the batch against both its own writes and the
// materialized state. It returns the first rejection found.
func (r *Register) validateLocked(writes []Write) *RejectError {
	batchSeqs := make(map[string]map[int64]struct{}, len(writes))
	for i, w := range writes {
		switch {
		case strings.TrimSpace(w.Key) == "":
			return &RejectError{Index: i, Write: w, Err: ErrEmptyKey}
		case w.Seq <= 0:
			return &RejectError{Index: i, Write: w, Err: ErrInvalidSeq}
		}
		if current, ok := r.entries[w.Key]; ok && w.Seq == current.Seq {
			return &RejectError{Index: i, Write: w, Err: ErrDuplicateSeq}
		}
		seqs := batchSeqs[w.Key]
		if seqs == nil {
			seqs = map[int64]struct{}{}
			batchSeqs[w.Key] = seqs
		}
		if _, dup := seqs[w.Seq]; dup {
			return &RejectError{Index: i, Write: w, Err: ErrDuplicateSeq}
		}
		seqs[w.Seq] = struct{}{}
	}
	return nil
}

// Lookup returns the effective entry for a key.
func (r *Register) Lookup(key string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[key]
	return e, ok
}

// Snapshot returns a deep copy of the current effective view.
func (r *Register) Snapshot() map[string]Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Entry, len(r.entries))
	for k, e := range r.entries {
		out[k] = e
	}
	return out
}

// Discarded returns the cumulative count of late-write losers.
func (r *Register) Discarded() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.discarded
}

// ChangeLog returns a copy of the append-only change log.
func (r *Register) ChangeLog() []Change {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Change, len(r.log))
	copy(out, r.log)
	return out
}

// Verify re-derives the effective view by replaying the append-only log and
// checks that every withdraw matches the entry materialized at that moment.
func (r *Register) Verify() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	derived := make(map[string]Entry, len(r.entries))
	for i, c := range r.log {
		switch c.Kind {
		case ChangeWithdraw:
			current, ok := derived[c.Key]
			if !ok {
				return fmt.Errorf("register: log[%d] withdraw for key %q with no effective entry", i, c.Key)
			}
			if current.Seq != c.Seq || current.Value != c.Value {
				return fmt.Errorf("register: log[%d] withdraw (%d,%q) does not match materialized entry (%d,%q)",
					i, c.Seq, c.Value, current.Seq, current.Value)
			}
			delete(derived, c.Key)
		case ChangeEstablish:
			if current, ok := derived[c.Key]; ok && c.Seq >= current.Seq {
				return fmt.Errorf("register: log[%d] establish seq %d does not beat current seq %d for key %q",
					i, c.Seq, current.Seq, c.Key)
			}
			derived[c.Key] = Entry{Seq: c.Seq, Value: c.Value}
		default:
			return fmt.Errorf("register: log[%d] unknown change kind %q", i, c.Kind)
		}
	}

	if len(derived) != len(r.entries) {
		return fmt.Errorf("register: replayed key count %d != materialized key count %d",
			len(derived), len(r.entries))
	}
	for k, e := range r.entries {
		if d, ok := derived[k]; !ok || d != e {
			return fmt.Errorf("register: replayed entry for key %q = %+v, want %+v", k, d, e)
		}
	}
	return nil
}
