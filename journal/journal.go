// Package journal is an append-only, in-process execution trail. Each record
// is a length-prefixed frame protected by CRC32 so that a torn trailing write
// is deterministically discarded on replay. It depends on no other package.
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Phase labels the lifecycle stage carried by a record.
type Phase uint8

const (
	Started     Phase = 1 // an execution attempt began
	Succeeded   Phase = 2 // execution completed successfully
	Failed      Phase = 3 // an execution attempt failed (may be followed by retry)
	CompStart   Phase = 4 // a compensation attempt began
	Compensated Phase = 5 // compensation completed successfully
	CompFailed  Phase = 6 // a compensation attempt failed
)

// Record is one journal entry.
type Record struct {
	Seq     uint64
	StepID  string
	Phase   Phase
	Attempt uint32
}

// ErrLimit is returned when appending would exceed the configured byte limit.
var ErrLimit = errors.New("journal: length limit exceeded")

// CrashFunc is invoked around the actual byte write. Returning an error aborts
// the append before any state changes. To simulate a crash mid-write the hook
// may additionally call TruncatePrefix to leave a torn frame on disk.
type CrashFunc func(nextSeq uint64, when int) error

const (
	// BeforeWrite / AfterWrite identify the two crash points per record.
	BeforeWrite = 0
	AfterWrite  = 1

	headerLen = 21 // 8 seq + 4 idlen + 1 phase + 4 attempt + 4 crc
)

// Journal is the append log and replay engine.
type Journal struct {
	data     []byte
	maxBytes int
	seq      uint64
	crash    CrashFunc
}

// New creates a journal. data may be existing bytes (recovery); maxBytes <= 0
// means unlimited. crash may be nil.
func New(data []byte, maxBytes int, crash CrashFunc) *Journal {
	j := &Journal{data: append([]byte(nil), data...), maxBytes: maxBytes, crash: crash}
	recs, valid := j.scan()
	if !valid {
		off := 0
		for _, r := range recs {
			off += headerLen + len(r.StepID)
		}
		j.data = j.data[:off]
	}
	if n := len(recs); n > 0 {
		j.seq = recs[n-1].Seq
	}
	return j
}

// Bytes returns the raw journal bytes (usable as recovery input).
func (j *Journal) Bytes() []byte { return append([]byte(nil), j.data...) }

// Len returns the current journal length in bytes.
func (j *Journal) Len() int { return len(j.data) }

// SetHook replaces the crash hook.
func (j *Journal) SetHook(f CrashFunc) { j.crash = f }

// TruncatePrefix keeps only the first n raw bytes, simulating a torn write.
func (j *Journal) TruncatePrefix(n int) {
	if n < 0 {
		n = 0
	}
	if n > len(j.data) {
		n = len(j.data)
	}
	j.data = j.data[:n]
}

func encode(r Record) []byte {
	id := []byte(r.StepID)
	frame := make([]byte, headerLen+len(id))
	binary.BigEndian.PutUint64(frame[0:8], r.Seq)
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(id)))
	frame[12] = byte(r.Phase)
	binary.BigEndian.PutUint32(frame[13:17], r.Attempt)
	binary.BigEndian.PutUint32(frame[17:21], crc32.ChecksumIEEE(frame[:17]))
	copy(frame[21:], id)
	return frame
}

// Append writes one record, assigning the next sequence number. It enforces
// the byte limit before mutating anything.
func (j *Journal) Append(stepID string, phase Phase, attempt uint32) (Record, error) {
	r := Record{Seq: j.seq + 1, StepID: stepID, Phase: phase, Attempt: attempt}
	frame := encode(r)
	if j.maxBytes > 0 && len(j.data)+len(frame) > j.maxBytes {
		return Record{}, ErrLimit
	}
	if j.crash != nil {
		if err := j.crash(r.Seq, BeforeWrite); err != nil {
			return Record{}, err
		}
	}
	j.data = append(j.data, frame...)
	if j.crash != nil {
		_ = j.crash(r.Seq, AfterWrite)
	}
	j.seq = r.Seq
	return r, nil
}

// scan parses frames; returns records and the valid byte prefix length.
func (j *Journal) scan() ([]Record, bool) {
	data := j.data
	var recs []Record
	off := 0
	for off < len(data) {
		if len(data)-off < headerLen {
			return recs, false
		}
		idLen := int(binary.BigEndian.Uint32(data[off+8 : off+12]))
		total := headerLen + idLen
		if len(data)-off < total {
			return recs, false
		}
		frame := data[off : off+total]
		want := binary.BigEndian.Uint32(frame[17:21])
		if crc32.ChecksumIEEE(frame[:17]) != want {
			return recs, false
		}
		recs = append(recs, Record{
			Seq:     binary.BigEndian.Uint64(frame[0:8]),
			StepID:  string(frame[21 : 21+idLen]),
			Phase:   Phase(frame[12]),
			Attempt: binary.BigEndian.Uint32(frame[13:17]),
		})
		off += total
	}
	return recs, true
}

// Replay returns all valid records; any torn trailing frame is discarded and
// the stored bytes are truncated to the valid prefix. Pure w.r.t. records:
// replaying the same bytes always yields the same result.
func (j *Journal) Replay() []Record {
	recs, valid := j.scan()
	if !valid {
		off := 0
		for _, r := range recs {
			off += headerLen + len(r.StepID)
		}
		j.data = j.data[:off]
	}
	return recs
}
