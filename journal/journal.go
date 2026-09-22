package journal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
)

// ErrLimit is returned when appending would exceed the configured record cap.
// It is a distinct, decidable error from all other failure modes.
var ErrLimit = errors.New("journal: length limit exceeded")

// ErrCrc means a whole-length frame failed checksum verification.
var ErrCrc = errors.New("journal: corrupted frame (crc mismatch)")

// CrashPoint identifies one of the three physical write boundaries.
type CrashPoint uint8

const (
	BeforeWrite CrashPoint = iota // frame not started
	MidWrite                      // only the first half of the frame is durable
	AfterWrite                    // frame fully durable
)

func (p CrashPoint) String() string {
	switch p {
	case BeforeWrite:
		return "before-write"
	case MidWrite:
		return "mid-write"
	case AfterWrite:
		return "after-write"
	default:
		return "unknown"
	}
}

// CrashHook is invoked at every physical write boundary. A hook that wants
// to simulate a crash panics with a value the embedding process treats as
// fatal; the journal itself never interprets the panic.
type CrashHook func(r Record, point CrashPoint)

// Journal is an in-memory append-only trail. The byte buffer models
// durable storage: it is the only state consulted during recovery.
type Journal struct {
	buf     []byte // physical frames
	records []Record
	limit   int // 0 means unlimited
	hook    CrashHook
}

// New creates an empty journal with an optional record-count limit and an
// optional crash hook (either may be zero/nil).
func New(limit int, hook CrashHook) *Journal {
	return &Journal{limit: limit, hook: hook}
}

// frame layout: [4 payloadLen][4 crc32(payload)][payload]
// payload is the canonical text form of one Record.

func encodeRecord(r Record) []byte {
	return []byte(fmt.Sprintf("%d\t%d\t%s\t%s", r.Seq, r.Phase, r.StepID, r.Detail))
}

func decodeRecord(p []byte) (Record, error) {
	parts := strings.SplitN(string(p), "\t", 4)
	if len(parts) != 4 {
		return Record{}, errors.New("journal: malformed payload")
	}
	var r Record
	if _, err := fmt.Sscanf(parts[0], "%d", &r.Seq); err != nil {
		return Record{}, err
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &r.Phase); err != nil {
		return Record{}, err
	}
	r.StepID = parts[2]
	r.Detail = parts[3]
	if r.Seq <= 0 || r.Phase < PhaseStart || r.Phase > PhaseCompFailed || r.StepID == "" {
		return Record{}, errors.New("journal: invalid record fields")
	}
	r.Phase = Phase(r.Phase)
	return r, nil
}

func buildFrame(r Record) []byte {
	payload := encodeRecord(r)
	frame := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)
	return frame
}

// Append assigns the next sequence number, persists one frame, and returns
// the stored record. Rejection leaves the journal byte-for-byte unchanged.
// A MidWrite hook crash leaves exactly half a frame on the storage image.
func (j *Journal) Append(stepID string, phase Phase, detail string) (Record, error) {
	if stepID == "" {
		return Record{}, errors.New("journal: empty step id")
	}
	if j.limit > 0 && len(j.records) >= j.limit {
		return Record{}, ErrLimit
	}
	r := Record{Seq: len(j.records) + 1, StepID: stepID, Phase: phase, Detail: detail}
	frame := buildFrame(r)
	if j.hook != nil {
		j.hook(r, BeforeWrite)
	}
	half := len(frame) / 2
	j.buf = append(j.buf, frame[:half]...)
	if j.hook != nil {
		j.hook(r, MidWrite)
	}
	j.buf = append(j.buf, frame[half:]...)
	if j.hook != nil {
		j.hook(r, AfterWrite)
	}
	j.records = append(j.records, r)
	return r, nil
}

// Len returns the number of durable, verified records.
func (j *Journal) Len() int { return len(j.records) }

// Snapshot returns a defensive copy of all records in sequence order.
func (j *Journal) Snapshot() []Record {
	out := make([]Record, len(j.records))
	copy(out, j.records)
	return out
}

// Bytes returns the raw storage image (used to seed a fresh journal after
// a simulated process restart).
func (j *Journal) Bytes() []byte { return append([]byte(nil), j.buf...) }

// Load parses a storage image. A trailing partial frame (crash mid-write)
// is deterministically discarded; a complete frame failing its checksum is
// a hard corruption error. The parsed journal uses the supplied hook/limit.
func Load(image []byte, limit int, hook CrashHook) (*Journal, error) {
	j := &Journal{limit: limit, hook: hook}
	data := append([]byte(nil), image...)
	for len(data) > 0 {
		if len(data) < 8 {
			break // torn header: discard
		}
		n := int(binary.BigEndian.Uint32(data[0:4]))
		total := 8 + n
		if len(data) < total {
			break // torn payload: discard the incomplete tail
		}
		frame := data[:total]
		payload := frame[8:]
		if binary.BigEndian.Uint32(frame[4:8]) != crc32.ChecksumIEEE(payload) {
			return nil, fmt.Errorf("%w: seq slot %d", ErrCrc, len(j.records)+1)
		}
		r, err := decodeRecord(payload)
		if err != nil {
			return nil, err
		}
		if r.Seq != len(j.records)+1 {
			return nil, fmt.Errorf("journal: non-contiguous sequence: want %d got %d", len(j.records)+1, r.Seq)
		}
		j.buf = append(j.buf, frame...)
		j.records = append(j.records, r)
		data = data[total:]
	}
	return j, nil
}
