// Package journal is an append-only, in-process execution journal. It is the
// single source of truth for workflow recovery: replay folds its records and
// nothing else. It depends on no other package in this module.
package journal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Phase is the lifecycle phase recorded for a step.
type Phase string

const (
	// ExecStart / ExecDone bracket one execution attempt.
	ExecStart Phase = "exec_start"
	ExecDone  Phase = "exec_done"
	// CompStart / CompDone bracket one compensation attempt.
	CompStart Phase = "comp_start"
	CompDone  Phase = "comp_done"
	// Compensating marks the global switch into compensation mode.
	Compensating Phase = "compensating"
	// AllCompleted / AllCompensated are the two global terminal phases.
	AllCompleted   Phase = "all_completed"
	AllCompensated Phase = "all_compensated"
)

// Record is one journal entry. Seq is assigned by the journal at append time;
// the first record has Seq 1.
type Record struct {
	Seq     int    `json:"seq"`
	StepID  string `json:"step"`
	Phase   Phase  `json:"phase"`
	OK      bool   `json:"ok"`
	Err     string `json:"err,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

// ErrLimit is returned when appending would exceed the configured maximum
// number of journal records.
var ErrLimit = errors.New("journal: length limit exceeded")

// ErrCrashed is returned by any append after a Crash has been injected; the
// process is simulated as dead until Recover restarts it.
var ErrCrashed = errors.New("journal: process crashed")

// CrashPoint selects where a crash happens during an append.
type CrashPoint int

const (
	// BeforeWrite crashes before any byte of the record is written.
	BeforeWrite CrashPoint = iota
	// MidWrite writes a strict prefix of the frame, simulating a torn write.
	MidWrite
	// AfterWrite crashes after the whole frame is durable.
	AfterWrite
)

// CrashHook decides whether the next append crashes and where. Returning a
// non-nil hook arms a one-shot crash.
type CrashHook func(rec Record, writeSeq int) (CrashPoint, bool)

// Store is a persistent (for the life of the process simulation) byte buffer.
// A crash does not clear it; Recover reads it back, exactly like a disk file.
type Store struct {
	mu  sync.Mutex
	buf []byte
}

// NewStore creates an empty backing store.
func NewStore() *Store { return &Store{} }

// Len returns the current on-disk byte length.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

// Snapshot returns a copy of the raw bytes.
func (s *Store) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf...)
}

// Truncate removes all bytes (used by tests simulating a fresh disk).
func (s *Store) Truncate() {
	s.mu.Lock()
	s.buf = nil
	s.mu.Unlock()
}

// Journal serializes record appends over one Store.
type Journal struct {
	mu         sync.Mutex
	store      *Store
	maxRecords int
	hook       CrashHook
	crashed    bool
}

// Options configures a journal. MaxRecords <= 0 means unlimited.
type Options struct {
	MaxRecords int
	Hook       CrashHook
}

// New opens (or reopens after recovery) a journal over the given store. No
// replay happens here: callers use Replay for that.
func New(store *Store, opts Options) *Journal {
	return &Journal{
		store:      store,
		maxRecords: opts.MaxRecords,
		hook:       opts.Hook,
	}
}

// Append writes one frame: big-endian uint32 length followed by JSON payload.
// It is linearizable with respect to Replay.
func (j *Journal) Append(rec Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.crashed {
		return Record{}, ErrCrashed
	}
	recs, torn := decodeAll(j.store.buf)
	nextSeq := len(recs) + 1
	if torn {
		// Recovery is responsible for truncating torn bytes; an append before
		// recovery would hide corruption, so refuse it.
		return Record{}, errors.New("journal: torn record present; recover first")
	}
	if j.maxRecords > 0 && nextSeq > j.maxRecords {
		return Record{}, ErrLimit
	}
	rec.Seq = nextSeq

	payload, err := json.Marshal(rec)
	if err != nil {
		return Record{}, fmt.Errorf("journal: encode: %w", err)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)

	if j.hook != nil {
		if point, yes := j.hook(rec, nextSeq); yes {
			switch point {
			case BeforeWrite:
				j.crashed = true
				return Record{}, ErrCrashed
			case MidWrite:
				cut := len(frame) / 2
				if cut < 1 {
					cut = 1
				}
				j.store.mu.Lock()
				j.store.buf = append(j.store.buf, frame[:cut]...)
				j.store.mu.Unlock()
				j.crashed = true
				return Record{}, ErrCrashed
			case AfterWrite:
				j.store.mu.Lock()
				j.store.buf = append(j.store.buf, frame...)
				j.store.mu.Unlock()
				j.crashed = true
				return Record{}, ErrCrashed
			}
		}
	}

	j.store.mu.Lock()
	j.store.buf = append(j.store.buf, frame...)
	j.store.mu.Unlock()
	return rec, nil
}

// Recover simulates process restart: the journal is reopened and any trailing
// torn frame is deterministically discarded. It returns the number of complete
// records retained and the number of torn bytes removed.
func Recover(store *Store, opts Options) (*Journal, int, int, error) {
	store.mu.Lock()
	recs, torn := decodeAll(store.buf)
	tornBytes := 0
	if torn {
		// Keep only the prefix of complete frames.
		var keep bytes.Buffer
		for _, r := range recs {
			b, err := json.Marshal(r)
			if err != nil {
				store.mu.Unlock()
				return nil, 0, 0, err
			}
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
			keep.Write(hdr[:])
			keep.Write(b)
		}
		tornBytes = len(store.buf) - keep.Len()
		store.buf = keep.Bytes()
	}
	store.mu.Unlock()
	return New(store, opts), len(recs), tornBytes, nil
}

// Replay folds all complete records in order. The callback must be cheap; it
// is invoked once per record and the journal performs no graph work here.
func Replay(store *Store, fn func(Record) error) (int, error) {
	store.mu.Lock()
	recs, torn := decodeAll(store.buf)
	store.mu.Unlock()
	if torn {
		return 0, errors.New("journal: cannot replay with torn record; recover first")
	}
	for _, r := range recs {
		if err := fn(r); err != nil {
			return r.Seq, err
		}
	}
	return len(recs), nil
}

// Len returns the number of complete records currently in the journal.
func (j *Journal) Len() int {
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	recs, _ := decodeAll(j.store.buf)
	return len(recs)
}

// SetHook replaces the crash hook (mainly for tests rearming crashes).
func (j *Journal) SetHook(h CrashHook) {
	j.mu.Lock()
	j.hook = h
	j.mu.Unlock()
}

// Crashed reports whether a simulated crash has occurred.
func (j *Journal) Crashed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.crashed
}

// decodeAll parses frames. It returns complete records and reports torn=true
// when trailing bytes cannot form one full, self-consistent frame.
func decodeAll(buf []byte) ([]Record, bool) {
	var recs []Record
	for len(buf) > 0 {
		if len(buf) < 4 {
			return recs, true
		}
		n := int(binary.BigEndian.Uint32(buf[:4]))
		if len(buf) < 4+n {
			return recs, true
		}
		var r Record
		if err := json.Unmarshal(buf[4:4+n], &r); err != nil {
			return recs, true
		}
		recs = append(recs, r)
		buf = buf[4+n:]
	}
	return recs, false
}

// AppendRaw appends arbitrary bytes directly to the store. Test-only helper
// for simulating corruption/torn tails without the hook machinery.
func (s *Store) AppendRaw(p []byte) {
	s.mu.Lock()
	s.buf = append(s.buf, p...)
	s.mu.Unlock()
}
