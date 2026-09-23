// Package journal is an append-only execution trail living in process memory.
// Each record carries a step id, a phase and a sequence number. Frames are
// length-prefixed and CRC-protected so a torn tail write is deterministically
// discarded on replay. It depends on no other package in this module.
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// ErrJournalFull is returned when appending would exceed the length cap.
var ErrJournalFull = errors.New("journal: length limit exceeded")

// Phase is the lifecycle phase recorded per step.
type Phase uint8

const (
	ExecStart Phase = 1 + iota
	ExecDone
	ExecFail
	CompStart
	CompDone
	CompFail
)

// Record is one trail entry.
type Record struct {
	Seq    int
	StepID string
	Phase  Phase
	Attempt int
}

// CrashHook is invoked around every physical frame write. "before" is true just
// before the bytes enter the buffer, false just after (before any in-memory
// semantics are attached). A non-nil return simulates a crash: it panics with
// that value.
type CrashHook func(rec Record, before bool)

// Journal is the append-only byte buffer plus its replay view.
type Journal struct {
	buf     []byte
	maxRecs int
	seq     int
	onWrite CrashHook
}

// New creates a journal with a record cap (<=0 means unlimited).
func New(maxRecs int) *Journal { return &Journal{maxRecs: maxRecs} }

// SetCrashHook installs the crash hook (pass nil to clear).
func (j *Journal) SetCrashHook(h CrashHook) { j.onWrite = h }

// Len is the number of durable (complete) records.
func (j *Journal) Len() int { return j.seq }

// Bytes exposes the raw buffer (used to fork state across a simulated crash).
func (j *Journal) Bytes() []byte { return append([]byte(nil), j.buf...) }

// Restore replaces the raw bytes from a previous Bytes() snapshot; sequence is
// recovered by replay, so no in-memory residue is relied upon.
func (j *Journal) Restore(b []byte) {
	j.buf = append([]byte(nil), b...)
	j.seq = len(Replay(j.buf))
}

// Append encodes and durably appends one record. On ErrJournalFull nothing is
// written. A crash hook panic leaves the buffer exactly as it was before the
// write point (before) or with the complete frame (after).
func (j *Journal) Append(id string, p Phase, attempt int) (Record, error) {
	if j.maxRecs > 0 && j.seq >= j.maxRecs {
		return Record{}, ErrJournalFull
	}
	j.seq++
	rec := Record{Seq: j.seq, StepID: id, Phase: p, Attempt: attempt}
	payload := encode(rec)
	frame := make([]byte, 4+len(payload)+4)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	binary.BigEndian.PutUint32(frame[4+len(payload):], crc32.ChecksumIEEE(payload))
	if j.onWrite != nil {
	j.onWrite(rec, true)
	}
	j.buf = append(j.buf, frame...)
	if j.onWrite != nil {
	j.onWrite(rec, false)
	}
	return rec, nil
}

func encode(r Record) []byte {
	idb := []byte(r.StepID)
	b := make([]byte, 0, 16+len(idb))
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(r.Seq))
	b = append(b, tmp[:]...)
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(idb)))
	b = append(b, tmp[:4]...)
	b = append(b, idb...)
	b = append(b, byte(r.Phase))
	binary.BigEndian.PutUint32(tmp[:4], uint32(r.Attempt))
	b = append(b, tmp[:4]...)
	return b
}

func decode(b []byte) Record {
	r := Record{}
	r.Seq = int(binary.BigEndian.Uint64(b[:8]))
	n := int(binary.BigEndian.Uint32(b[8:12]))
	r.StepID = string(b[12 : 12+n])
	r.Phase = Phase(b[12+n])
	r.Attempt = int(binary.BigEndian.Uint32(b[13+n : 17+n]))
	return r
}

// Replay folds raw bytes into records, deterministically dropping a torn tail:
// a truncated length header, a truncated payload/CRC, or a CRC mismatch (which
// can only occur at the crash tail) stops the scan there.
func Replay(buf []byte) []Record {
	var out []Record
	for len(buf) >= 8 {
		n := int(binary.BigEndian.Uint32(buf[:4]))
		total := 4 + n + 4
		if n < 9 || len(buf) < total {
			break
		}
		payload := buf[4 : 4+n]
		want := binary.BigEndian.Uint32(buf[4+n : total])
		if crc32.ChecksumIEEE(payload) != want {
			break
		}
		out = append(out, decode(payload))
		buf = buf[total:]
	}
	return out
}
