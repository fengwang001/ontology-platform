// Package log is an append-only in-memory log with monotonically increasing
// offsets and a separately maintained persistence point.
//
// It knows nothing about truncation markers or crash recovery: it only stores
// entries, advances the checkpoint, and can physically drop a prefix that the
// caller has already proven to be persisted. It depends on no other package.
package log

import (
	"errors"
	"sync"
)

// ErrCheckpointInvalid is returned when Checkpoint is called with an offset at
// or beyond the current append site (out of range), or below the already
// established persistence point (a retreat). The log is left untouched.
var ErrCheckpointInvalid = errors.New("log: checkpoint offset out of range or retreats")

// Entry is one log record. Offsets are dense and start at zero.
type Entry struct {
	Offset  uint64
	Payload string
}

type entry struct {
	payload string
}

// Log is safe for concurrent use. The empty value is not usable; use New.
type Log struct {
	mu      sync.RWMutex
	recs    []entry // live entries; recs[i] has offset first+uint64(i)
	first   uint64  // offset of recs[0]
	cp      uint64  // largest offset declared persisted, when cpValid
	cpValid bool
}

func New() *Log { return &Log{} }

// Append stores payload at the next dense offset and returns that offset.
func (l *Log) Append(payload string) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	off := l.first + uint64(len(l.recs))
	l.recs = append(l.recs, entry{payload: payload})
	return off, nil
}

// Checkpoint declares [0, off] persisted. off must be below the append site and
// must not retreat below the current persistence point.
func (l *Log) Checkpoint(off uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	site := l.first + uint64(len(l.recs))
	if off >= site || (l.cpValid && off < l.cp) {
		return ErrCheckpointInvalid
	}
	if !l.cpValid || off > l.cp {
		l.cp, l.cpValid = off, true
	}
	return nil
}

// CP reports the persistence point. The boolean is false before any successful
// Checkpoint. Truncation legality is decided by reading this single field.
func (l *Log) CP() (uint64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cp, l.cpValid
}

// First is the offset of the oldest physically present entry.
func (l *Log) First() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.first
}

// Tail is the current append site: one past the newest entry's offset.
func (l *Log) Tail() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.first + uint64(len(l.recs))
}

// DropBefore physically removes [0, k). It is legal only when k <= cp, so an
// entry can never disappear before it is persisted. Returns the new first
// offset; an illegal call changes nothing.
func (l *Log) DropBefore(k uint64) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.cpValid || k > l.cp {
		return l.first, ErrDropUnpersisted
	}
	if k <= l.first {
		return l.first, nil
	}
	n := k - l.first
	if n > uint64(len(l.recs)) {
		n = uint64(len(l.recs))
	}
	l.recs = append([]entry(nil), l.recs[n:]...)
	l.first += n
	return l.first, nil
}

// ErrDropUnpersisted is returned when a drop would remove entries not covered
// by the persistence point.
var ErrDropUnpersisted = errors.New("log: cannot drop entries not covered by checkpoint")

// Read returns live entries whose offset is >= from, in offset order. Entries
// already dropped are simply absent, matching the naive reference (append
// everything, then delete every truncated prefix).
func (l *Log) Read(from uint64) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if from < l.first {
		from = l.first
	}
	if from > l.first+uint64(len(l.recs)) {
		return nil
	}
	start := int(from - l.first)
	out := make([]Entry, 0, len(l.recs)-start)
	for i := start; i < len(l.recs); i++ {
		out = append(out, Entry{Offset: l.first + uint64(i), Payload: l.recs[i].payload})
	}
	return out
}
