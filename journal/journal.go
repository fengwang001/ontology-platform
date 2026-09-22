// Package journal implements the append-only execution trail. It is the
// single source of truth for recovery and depends on no other package.
package journal

import (
	"errors"
	"sync"
)

// ErrTooLong is returned when appending would exceed the record limit.
var ErrTooLong = errors.New("journal: record limit exceeded")

// Phase identifies the lifecycle stage a record describes.
type Phase int

const (
	PhaseStart     Phase = iota // step execution begins (first attempt)
	PhaseRetry                  // another attempt begins after a failure
	PhaseSuccess                // step finished successfully
	PhaseFailure                // step failed terminally (retries exhausted)
	PhaseCompStart              // compensation begins
	PhaseCompOK                 // compensation succeeded
	PhaseCompFail               // compensation failed terminally
)

// Record is one entry of the execution trail.
type Record struct {
	Seq    uint64
	Phase  Phase
	StepID string
	Note   string // optional detail, e.g. the error text
}

// When distinguishes the two crash-observable moments of one append.
type When int

const (
	Before When = iota // record not yet written
	After              // record fully written
)

// Event is delivered to a Hook around every append. Snapshot returns a
// copy of the journal bytes exactly as they are at that moment; it is
// lazy so observers that never crash pay nothing.
type Event struct {
	Record   Record
	When     When
	Snapshot func() []byte
}

// Hook observes appends; used to inject crashes before/after any record.
type Hook func(Event)

// Journal is an in-memory append-only trail of framed, checksummed
// records. It is safe for concurrent use.
type Journal struct {
	mu         sync.Mutex
	buf        []byte
	count      int
	seq        uint64
	maxRecords int // 0 means unlimited
	hook       Hook
}

// New creates an empty journal. maxRecords <= 0 means unlimited.
func New(maxRecords int, hook Hook) *Journal {
	return &Journal{maxRecords: maxRecords, hook: hook}
}

// FromBytes rebuilds a journal from raw bytes. A torn or corrupt tail
// (half-written record) is detected and dropped; torn reports whether
// that happened. The journal keeps only the valid prefix, so later
// appends always extend a self-consistent log.
func FromBytes(b []byte, maxRecords int, hook Hook) (*Journal, bool) {
	j := New(maxRecords, hook)
	valid, torn := validPrefix(b)
	j.buf = append(j.buf, b[:valid]...)
	for _, r := range decodeAll(j.buf) {
		j.count++
		if r.Seq > j.seq {
			j.seq = r.Seq
		}
	}
	return j, torn
}

// Append frames and writes one record, invoking the hook immediately
// before and after the write while holding the journal lock.
func (j *Journal) Append(phase Phase, stepID, note string) (rec Record, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.maxRecords > 0 && j.count >= j.maxRecords {
		return rec, ErrTooLong
	}
	rec = Record{Seq: j.seq + 1, Phase: phase, StepID: stepID, Note: note}
	if j.hook != nil {
		j.hook(Event{Record: rec, When: Before, Snapshot: j.snapshotLocked})
	}
	j.buf = append(j.buf, encodeRecord(rec)...)
	j.count++
	j.seq = rec.Seq
	if j.hook != nil {
		j.hook(Event{Record: rec, When: After, Snapshot: j.snapshotLocked})
	}
	return rec, nil
}

func (j *Journal) snapshotLocked() []byte {
	out := make([]byte, len(j.buf))
	copy(out, j.buf)
	return out
}

// Records replays the journal: it decodes every frame in order. The
// journal's own buffer is always a valid prefix, so this never fails.
func (j *Journal) Records() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	return decodeAll(j.buf)
}

// Len returns the number of records currently stored.
func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.count
}

// Bytes returns a copy of the raw log.
func (j *Journal) Bytes() []byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshotLocked()
}

func decodeAll(buf []byte) []Record {
	var out []Record
	for len(buf) > 0 {
		rec, n, ok := decodeFrame(buf)
		if !ok {
			break
		}
		out = append(out, rec)
		buf = buf[n:]
	}
	return out
}

// validPrefix returns the length of the longest prefix of b that decodes
// into complete, CRC-valid frames, and whether anything had to be dropped.
func validPrefix(b []byte) (int, bool) {
	off := 0
	for off < len(b) {
		_, n, ok := decodeFrame(b[off:])
		if !ok {
			return off, true
		}
		off += n
	}
	return off, false
}
