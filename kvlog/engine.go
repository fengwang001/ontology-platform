package kvlog

import (
	"os"
	"sync"
	"sync/atomic"
)

// Config tunes an Engine.
type Config struct {
	// MaxSegmentBytes seals and rolls the active segment once it is exceeded.
	// Zero defaults to 1 MiB.
	MaxSegmentBytes int
	// WriteHints enables hint generation when sealing segments.
	WriteHints bool
}

// Status is the read result kind.
type Status int

const (
	// StatusMissing means the key never appeared in the live keyspace.
	StatusMissing Status = iota
	// StatusDeleted means the newest record for the key is a tombstone.
	StatusDeleted
	// StatusPresent means the key has a live value.
	StatusPresent
)

// Result is returned by Get.
type Result struct {
	Status Status
	Value  []byte
}

// segment is the runtime view of one log segment.
type segment struct {
	id     int
	sealed bool
	// hintAdopted says the keydir entries were built from the hint, so
	// record bytes were never verified during recovery.
	hintAdopted bool
	// latest maps key -> newest frame within this segment (for hint
	// generation at seal time and for re-scans).
	latest map[string]frame
	size   int64
}

// Engine is the append-only key/value engine.
type Engine struct {
	dir        string
	cfg        Config
	mu         sync.RWMutex
	segs       []*segment // ascending by id
	segIndex   map[int]*segment
	dir2       *keyDir
	nextSeq    uint64
	nextSegID  int
	tornBytes  int64
	selfHeals  uint64
	activeFile *os.File
	logger     Logger

	// bytesRead counts segment data bytes read since open (test support).
	bytesRead atomic.Int64
	// crashHook, when set, is invoked at named crash points; returning true
	// simulates an immediate process exit: the process exits with code 42.
	crashHook func(point string) bool
}

// Logger receives one structured-ish line per operation for reproducibility.
type Logger interface {
	Logf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

// Open (skeleton; implemented in recovery.go).
// Close releases engine resources.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeFile != nil {
		err := e.activeFile.Close()
		e.activeFile = nil
		return err
	}
	return nil
}

// NextSeq returns the next sequence number that will be assigned.
func (e *Engine) NextSeq() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.nextSeq
}

// TornBytes reports bytes discarded from a torn active-segment tail.
func (e *Engine) TornBytes() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tornBytes
}

// SelfHeals reports the count of keydir-distortion self-healing events.
func (e *Engine) SelfHeals() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.selfHeals
}

// BytesRead reports segment data bytes read since Open (test support).
func (e *Engine) BytesRead() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.bytesRead.Load()
}

// SetLogger installs an operation logger.
func (e *Engine) SetLogger(l Logger) {
	if l == nil {
		l = nopLogger{}
	}
	e.mu.Lock()
	e.logger = l
	e.mu.Unlock()
}
