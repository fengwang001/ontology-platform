// Package ontology implements versioned conditional writes with delete
// tombstones, a global watermark and version-based tombstone retention.
package ontology

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
)

// Op identifies the kind of change carried by an Event.
type Op int

const (
	OpWrite Op = iota + 1
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpWrite:
		return "WRITE"
	case OpDelete:
		return "DELETE"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(o))
	}
}

// Event is a possibly out-of-order change for a single key.
type Event struct {
	Key     string
	Version int64
	Op      Op
	Value   string
}

// Row is a live key/value entry with its winning version.
type Row struct {
	Key     string
	Value   string
	Version int64
}

// Tombstone records the version of the latest delete for an absent key.
type Tombstone struct {
	Key     string
	Version int64
}

// Config configures a Store.
type Config struct {
	// RetentionVersions keeps a tombstone while watermark - stoneVersion
	// is strictly less than this value. Zero means immediate eviction.
	RetentionVersions int64
	// MaxEntries limits the number of live rows. Zero means unlimited.
	MaxEntries int
}

// Store is the concurrency-safe versioned key/value store.
//
// A key's current version is the version of its live row, or of its
// tombstone if the key was deleted, or zero when unknown. An event is
// applied only when its version is strictly greater than the current
// version. Writes install/replace the row and clear the tombstone;
// deletes remove the row and install a tombstone, even for unknown keys.
//
// The watermark is the maximum version seen across every accepted event.
// After each applied batch, every tombstone with watermark - stoneVersion
// greater than or equal to RetentionVersions is evicted, returning that
// key to the unknown state.
type Store struct {
	mu        sync.RWMutex
	retention int64
	maxRows   int
	rows      map[string]Row
	stones    map[string]int64
	watermark int64
	ignored   int64
	logger    *log.Logger
}

// New constructs an empty Store. It panics on an illegal Config so that a
// misconfigured process fails fast rather than silently dropping data.
func New(cfg Config) *Store {
	if cfg.RetentionVersions < 0 {
		panic("ontology: RetentionVersions must be >= 0")
	}
	if cfg.MaxEntries < 0 {
		panic("ontology: MaxEntries must be >= 0")
	}
	return &Store{
		retention: cfg.RetentionVersions,
		maxRows:   cfg.MaxEntries,
		rows:      map[string]Row{},
		stones:    map[string]int64{},
		logger:    log.New(io.Discard, "", 0),
	}
}

// WithLogger attaches a step logger. A nil writer disables logging.
func (s *Store) WithLogger(w io.Writer) *Store {
	if w == nil {
		w = io.Discard
	}
	s.mu.Lock()
	s.logger = log.New(w, "ontology ", log.LstdFlags|log.Lmicroseconds)
	s.mu.Unlock()
	return s
}

// Decision records what happened to a single validated event.
type Decision struct {
	Event          Event
	CurrentVersion int64
	Applied        bool
	Ignored        bool
	Outcome        string
}

// Apply validates and atomically applies a batch of events.
//
// The whole batch is rejected without any state change when an argument
// or event is illegal, or when committing it could exceed the configured
// live-row limit.
func (s *Store) Apply(events []Event) error {
	if len(events) == 0 {
		return &BatchError{Reason: ReasonInvalidArgument, Index: -1,
			Msg: "batch must contain at least one event"}
	}
	for i, ev := range events {
		switch {
		case ev.Key == "":
			return &BatchError{Reason: ReasonInvalidEvent, Index: i,
				Msg: fmt.Sprintf("event %d: key must not be empty", i)}
		case ev.Version <= 0:
			return &BatchError{Reason: ReasonInvalidEvent, Index: i,
				Msg: fmt.Sprintf("event %d: version must be > 0, got %d", i, ev.Version)}
		case ev.Op != OpWrite && ev.Op != OpDelete:
			return &BatchError{Reason: ReasonInvalidEvent, Index: i,
				Msg: fmt.Sprintf("event %d: unknown op %d for key %q", i, ev.Op, ev.Key)}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Project the post-batch live-row set using the exact same version
	// rule, so ignored events and deletes cannot trip the limit.
	projectedRows := make(map[string]struct{}, len(s.rows))
	for k := range s.rows {
		projectedRows[k] = struct{}{}
	}
	for i, ev := range events {
		if ev.Version <= s.currentVersionLocked(ev.Key) {
			continue
		}
		switch ev.Op {
		case OpWrite:
			projectedRows[ev.Key] = struct{}{}
		case OpDelete:
			delete(projectedRows, ev.Key)
		}
		if s.maxRows > 0 && len(projectedRows) > s.maxRows {
			return &BatchError{Reason: ReasonLimitExceeded, Index: i,
				Msg: fmt.Sprintf("event %d: live rows would exceed MaxEntries %d", i, s.maxRows)}
		}
	}

	// All checks passed: commit under the same write lock.
	var batchMax int64
	for _, ev := range events {
		if ev.Version > batchMax {
			batchMax = ev.Version
		}
		curVersion := s.currentVersionLocked(ev.Key)
		d := Decision{Event: ev, CurrentVersion: curVersion}
		if ev.Version <= curVersion {
			s.ignored++
			d.Ignored = true
			_, hasRow := s.rows[ev.Key]
			_, hasStone := s.stones[ev.Key]
			switch {
			case hasStone:
				d.Outcome = "ignored: version not newer than tombstone"
			case hasRow:
				d.Outcome = "ignored: version not newer than live row"
			default:
				d.Outcome = "ignored: key unknown but version not positive"
			}
			s.logDecision(d)
			continue
		}
		d.Applied = true
		switch ev.Op {
		case OpWrite:
			s.rows[ev.Key] = Row{Key: ev.Key, Value: ev.Value, Version: ev.Version}
			delete(s.stones, ev.Key)
			d.Outcome = "applied WRITE: row installed/replaced, tombstone cleared"
		case OpDelete:
			delete(s.rows, ev.Key)
			s.stones[ev.Key] = ev.Version
			if curVersion == 0 {
				d.Outcome = "applied DELETE: tombstone installed for unknown key"
			} else {
				d.Outcome = "applied DELETE: row removed, tombstone installed"
			}
		}
		s.logDecision(d)
	}

	if batchMax > s.watermark {
		s.watermark = batchMax
	}
	s.sweepLocked()
	s.logger.Printf("batch committed: events=%d ignored_total=%d watermark=%d live_rows=%d tombstones=%d",
		len(events), s.ignored, s.watermark, len(s.rows), len(s.stones))
	return nil
}

// currentVersionLocked returns the live-row or tombstone version, else 0.
func (s *Store) currentVersionLocked(key string) int64 {
	if row, ok := s.rows[key]; ok {
		return row.Version
	}
	if v, ok := s.stones[key]; ok {
		return v
	}
	return 0
}

// sweepLocked evicts tombstones with watermark - version >= retention.
func (s *Store) sweepLocked() {
	for k, v := range s.stones {
		if s.watermark-v >= s.retention {
			delete(s.stones, k)
			s.logger.Printf("tombstone expired: key=%q stone_version=%d watermark=%d delta=%d retention=%d",
				k, v, s.watermark, s.watermark-v, s.retention)
		}
	}
}

func (s *Store) logDecision(d Decision) {
	s.logger.Printf("input: key=%q version=%d op=%s value=%q | current_version=%d -> %s",
		d.Event.Key, d.Event.Version, d.Event.Op, d.Event.Value,
		d.CurrentVersion, d.Outcome)
}

// CurrentVersion returns the version of the live row or tombstone, else 0.
func (s *Store) CurrentVersion(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentVersionLocked(key)
}

// Get returns the live row for a key and whether it exists. A tombstoned
// or unknown key reports false.
func (s *Store) Get(key string) (Row, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[key]
	return row, ok
}

// Rows returns a snapshot of all live rows ordered by key.
func (s *Store) Rows() []Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Tombstones returns a snapshot of all tombstones ordered by key.
func (s *Store) Tombstones() []Tombstone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tombstone, 0, len(s.stones))
	for k, v := range s.stones {
		out = append(out, Tombstone{Key: k, Version: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Watermark returns the maximum event version ever accepted.
func (s *Store) Watermark() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watermark
}

// Ignored returns the number of events rejected by the version rule.
func (s *Store) Ignored() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ignored
}

// Verify checks internal consistency; safe to call concurrently with Apply.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var problems []string
	for k, row := range s.rows {
		if _, buried := s.stones[k]; buried {
			problems = append(problems, fmt.Sprintf("key %q is both live and tombstoned", k))
		}
		if row.Key != k {
			problems = append(problems, fmt.Sprintf("row map key %q does not match row key %q", k, row.Key))
		}
		if row.Version <= 0 {
			problems = append(problems, fmt.Sprintf("live row %q has non-positive version %d", k, row.Version))
		}
	}
	if s.maxRows > 0 && len(s.rows) > s.maxRows {
		problems = append(problems, fmt.Sprintf("live rows %d exceed MaxEntries %d", len(s.rows), s.maxRows))
	}
	for k, v := range s.stones {
		if v <= 0 {
			problems = append(problems, fmt.Sprintf("tombstone %q has non-positive version %d", k, v))
		}
		if v > s.watermark {
			problems = append(problems, fmt.Sprintf("tombstone %q version %d above watermark %d", k, v, s.watermark))
		}
		if s.watermark-v >= s.retention {
			problems = append(problems, fmt.Sprintf("expired tombstone %q retained (delta %d >= retention %d)",
				k, s.watermark-v, s.retention))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("ontology self-check failed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
