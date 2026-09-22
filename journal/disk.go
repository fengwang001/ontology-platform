package journal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
)

// Errors returned by the journal layer. They are mutually distinguishable.
var (
	// ErrJournalFull means the configured record limit was reached.
	ErrJournalFull = errors.New("journal: length limit reached")
	// ErrCorrupt means a frame in the middle of the trail is malformed.
	ErrCorrupt = errors.New("journal: corrupt frame")
)

// CrashHook is invoked around every physical write. CrashBeforeWrite aborts
// before any byte of the new frame lands; CrashAfterWrite aborts after the
// bytes are durable. CrashHalfWrite appends a truncated frame first, which
// models a torn write. A hook that panics with ErrCrash simulates process
// death; the in-memory disk bytes survive, the orchestrator memory does not.
type CrashHook func(rec Record)

// Sentinel returned by crash hooks.
var ErrCrash = errors.New("journal: simulated crash")

// frame layout: magic(4) | length(4, big endian) | json payload | crc32(4).
var frameMagic = [4]byte{'J', 'R', 'N', 'L'}

// Disk is a process-memory-backed durable medium. A new Journal can be opened
// on the same Disk after a "crash"; nothing else about the old process is
// shared.
type Disk struct {
	data []byte
}

// NewDisk creates an empty medium.
func NewDisk() *Disk { return &Disk{} }

// Bytes exposes the raw storage for torn-write tests.
func (d *Disk) Bytes() []byte {
	out := make([]byte, len(d.data))
	copy(out, d.data)
	return out
}

func encodeFrame(rec Record) ([]byte, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(frameMagic[:])
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(payload)))
	buf.Write(lenbuf[:])
	buf.Write(payload)
	h := fnv.New32a()
	h.Write(payload)
	var crcbuf [4]byte
	binary.BigEndian.PutUint32(crcbuf[:], h.Sum32())
	buf.Write(crcbuf[:])
	return buf.Bytes(), nil
}

func parseFrame(buf []byte) (Record, int, error) {
	const header, footer = 8, 4
	if len(buf) < header+footer {
		return Record{}, 0, ErrCorrupt
	}
	if !bytes.Equal(buf[:4], frameMagic[:]) {
		return Record{}, 0, fmt.Errorf("%w: bad magic", ErrCorrupt)
	}
	n := int(binary.BigEndian.Uint32(buf[4:8]))
	if n < 0 || header+n+footer > len(buf) {
		return Record{}, 0, fmt.Errorf("%w: truncated frame", ErrCorrupt)
	}
	payload := buf[header : header+n]
	h := fnv.New32a()
	h.Write(payload)
	if binary.BigEndian.Uint32(buf[header+n:]) != h.Sum32() {
	return Record{}, 0, fmt.Errorf("%w: checksum mismatch", ErrCorrupt)
	}
	var rec Record
	if err := json.Unmarshal(payload, &rec); err != nil {
		return Record{}, 0, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return rec, header + n + footer, nil
}
