// Package fslog implements a forward-secure key-evolving audit log
// together with a one-pass checkpoint verifier.
package fslog

import (
	"strconv"
	"sync"
)

// Entry types.
const (
	TypData  uint64 = 0 // data record
	TypSeal  uint64 = 1 // seal record
	TypRekey uint64 = 2 // rekey record
)

// MaxTs is the largest accepted timestamp (10^15).
const MaxTs uint64 = 1_000_000_000_000_000

// Constructor parameter bounds.
const (
	MinCap     uint64 = 2
	MaxCap     uint64 = 1_000_000
	MaxMaxData int    = 4096
)

// Funcs bundles the three deterministic functions injected at construction.
type Funcs struct {
	Evolve func(k uint64) uint64
	Rekey  func(k uint64, i uint64) uint64
	Mac    func(k, i, typ, ts uint64, data []byte) uint64
}

// Entry is a single log record.
type Entry struct {
	Index uint64
	Typ   uint64
	Ts    uint64
	Data  []byte
	Tag   uint64
}

// OpKind classifies writer-side rejections.
type OpKind int

const (
	OpInvalidArg OpKind = iota
	OpSealed
	OpTimeRegression
	OpCapacityFull
)

// OpError is the error returned by rejected writer operations.
type OpError struct {
	Kind OpKind
	Msg  string
}

func (e *OpError) Error() string { return e.Msg }

func opErr(kind OpKind, msg string) *OpError { return &OpError{Kind: kind, Msg: msg} }

// Log is the writer-side forward-secure audit log.
type Log struct {
	mu      sync.Mutex
	f       Funcs
	cap     uint64
	maxData int
	next    uint64
	key     uint64
	lastTs  uint64
	sealed  bool
	entries []Entry
}

// New builds a Log; invalid configuration is rejected as a whole.
func New(f Funcs, k0, capacity uint64, maxData int) (*Log, error) {
	if f.Evolve == nil || f.Rekey == nil || f.Mac == nil {
		return nil, opErr(OpInvalidArg, "fslog: nil function in Funcs")
	}
	if capacity < MinCap || capacity > MaxCap {
		return nil, opErr(OpInvalidArg, "fslog: capacity out of [2, 10^6]")
	}
	if maxData < 0 || maxData > MaxMaxData {
		return nil, opErr(OpInvalidArg, "fslog: maxData out of [0, 4096]")
	}
	return &Log{f: f, cap: capacity, maxData: maxData, key: k0}, nil
}

// Append appends a data record.
func (l *Log) Append(ts uint64, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs || len(data) > l.maxData {
		return opErr(OpInvalidArg, "fslog: append: ts out of range or data too long")
	}
	if l.sealed {
		return opErr(OpSealed, "fslog: append: log is sealed")
	}
	if ts < l.lastTs {
		return opErr(OpTimeRegression, "fslog: append: ts before lastTs")
	}
	if l.next >= l.cap-1 {
		return opErr(OpCapacityFull, "fslog: append: capacity reserved for seal")
	}
	l.appendLocked(TypData, ts, data, false)
	return nil
}

// Seal appends the seal record; no writes are accepted afterwards.
func (l *Log) Seal(ts uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs {
		return opErr(OpInvalidArg, "fslog: seal: ts out of range")
	}
	if l.sealed {
		return opErr(OpSealed, "fslog: seal: log is sealed")
	}
	if ts < l.lastTs {
		return opErr(OpTimeRegression, "fslog: seal: ts before lastTs")
	}
	data := []byte(strconv.FormatUint(l.next, 10))
	l.appendLocked(TypSeal, ts, data, false)
	l.sealed = true
	return nil
}

// Rekey appends a rekey record and switches to rekey(k, i).
func (l *Log) Rekey(ts uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts > MaxTs {
		return opErr(OpInvalidArg, "fslog: rekey: ts out of range")
	}
	if l.sealed {
		return opErr(OpSealed, "fslog: rekey: log is sealed")
	}
	if ts < l.lastTs {
		return opErr(OpTimeRegression, "fslog: rekey: ts before lastTs")
	}
	if l.next >= l.cap-1 {
		return opErr(OpCapacityFull, "fslog: rekey: capacity reserved for seal")
	}
	data := []byte(strconv.FormatUint(l.next, 10))
	l.appendLocked(TypRekey, ts, data, true)
	return nil
}

// Export returns the current (i, ki) for checkpoint custody.
func (l *Log) Export() (uint64, uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return 0, 0, opErr(OpSealed, "fslog: export: log is sealed")
	}
	return l.next, l.key, nil
}

// Entries returns a copy of the current entries.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return cloneEntries(l.entries)
}

// SelfVerify verifies a consistent snapshot of the log's own entries.
func (l *Log) SelfVerify(cps []Checkpoint) Report {
	l.mu.Lock()
	snap := cloneEntries(l.entries)
	f := l.f
	l.mu.Unlock()
	return Verify(snap, cps, f)
}

// appendLocked appends one record and advances the key exactly once.
func (l *Log) appendLocked(typ uint64, ts uint64, data []byte, rekey bool) {
	i := l.next
	tag := l.f.Mac(l.key, i, typ, ts, data)
	l.entries = append(l.entries, Entry{Index: i, Typ: typ, Ts: ts, Data: cloneBytes(data), Tag: tag})
	if rekey {
		l.key = l.f.Rekey(l.key, i)
	} else {
		l.key = l.f.Evolve(l.key)
	}
	l.next++
	l.lastTs = ts
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func cloneEntries(es []Entry) []Entry {
	c := make([]Entry, len(es))
	for i, e := range es {
		c[i] = e
		c[i].Data = cloneBytes(e.Data)
	}
	return c
}
