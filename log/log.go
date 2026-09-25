// Package log implements the append-only, hash-chained audit log.
// It depends only on ent. There is intentionally no update or delete API.
package log

import (
	"errors"
	"sync"

	"ontology/ent"
)

// Sentinel errors for the four distinguishable rejection causes.
var (
	ErrNegativeTS = errors.New("audit: negative timestamp")
	ErrEmptyWho   = errors.New("audit: empty who")
	ErrEmptyOp    = errors.New("audit: empty op")
	ErrOutOfOrder = errors.New("audit: timestamp before previous entry")
)

// Log is an append-only audit log safe for concurrent use.
type Log struct {
	mu       sync.RWMutex
	entries  []ent.Entry
	head     [32]byte // cached hash of the last entry (chain head)
	lastRead int      // entries read to obtain the predecessor hash in the last Append
}

// New returns a log containing only the genesis entry.
func New() *Log {
	g := ent.Genesis()
	return &Log{entries: []ent.Entry{g}, head: g.Hash}
}

// NewFrom loads a log from existing storage verbatim (no re-hashing), so
// tampered storage can be inspected with Verify and Affected.
func NewFrom(entries []ent.Entry) *Log {
	l := &Log{entries: append([]ent.Entry(nil), entries...)}
	if n := len(entries); n > 0 {
		l.head = entries[n-1].Hash
	}
	return l
}

// Append validates and appends one entry, returning the assigned Seq.
// A rejected Append changes no state whatsoever.
func (l *Log) Append(ts int64, who, op string) (int64, error) {
	if ts < 0 {
		return 0, ErrNegativeTS
	}
	if who == "" {
		return 0, ErrEmptyWho
	}
	if op == "" {
		return 0, ErrEmptyOp
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := l.entries[len(l.entries)-1]
	if ts < prev.TS {
		return 0, ErrOutOfOrder
	}
	l.lastRead = 1 // only the cached chain head is consulted, never the whole chain
	seq := prev.Seq + 1
	e := ent.Entry{Seq: seq, TS: ts, Who: who, Op: op,
		Hash: ent.ComputeHash(l.head, seq, ts, who, op)}
	l.entries = append(l.entries, e)
	l.head = e.Hash
	return seq, nil
}

// Entries returns a copy of all entries, genesis first.
func (l *Log) Entries() []ent.Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]ent.Entry(nil), l.entries...)
}

// Verify recomputes every hash from genesis and returns the Seq of the
// first entry whose stored hash mismatches, or -1 if all match.
func (l *Log) Verify() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prev := ent.GenesisHash()
	for i, e := range l.entries {
		if i == 0 { // genesis is fully fixed: fields and hash must match exactly
			if e != ent.Genesis() {
				return e.Seq
			}
			continue
		}
		if ent.ComputeHash(prev, e.Seq, e.TS, e.Who, e.Op) != e.Hash {
			return e.Seq
		}
		prev = e.Hash
	}
	return -1
}

// Affected returns the Seq of every entry from seq to the end of the log:
// once an entry is tampered with, all later entries depend on its hash.
func (l *Log) Affected(seq int64) []int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []int64
	for _, e := range l.entries {
		if e.Seq >= seq {
			out = append(out, e.Seq)
		}
	}
	return out
}
