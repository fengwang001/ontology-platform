package versioned

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Logger is the minimal logging interface; *log.Logger satisfies it.
type Logger interface {
	Printf(format string, args ...any)
}

// WithLogger installs the logger used to print per-step inputs, live rows
// and decision rationale.
func WithLogger(l Logger) Option {
	return func(s *Store) { s.logger = l }
}

// Option configures a Store.
type Option func(*Store)

// Store applies versioned write/delete events with tombstones.
type Store struct {
	mu sync.RWMutex

	live  map[string]Row
	tombs map[string]int64

	watermark int64
	applied   int64
	ignored   int64

	retention int64
	maxBatch  int
	logger    Logger
}

// New creates a Store.
func New(retention int64, maxBatchSize int, opts ...Option) (*Store, error) {
	if retention < 0 {
		return nil, ErrInvalidRetention
	}
	if maxBatchSize <= 0 {
		return nil, ErrInvalidBatchLimit
	}
	s := &Store{
		live:      make(map[string]Row),
		tombs:     make(map[string]int64),
		retention: retention,
		maxBatch:  maxBatchSize,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Commit validates and atomically applies one batch of events.
//
// Events are applied in slice order. A rejected batch leaves no trace:
// live rows, tombstones, watermark and counters are unchanged.
func (s *Store) Commit(events []*Event) error {
	if err := s.validate(events); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, e := range events {
		if e.Version > s.watermark {
			s.watermark = e.Version
		}

		current, source := s.currentVersionLocked(e.Key)
		if e.Version <= current {
			s.ignored++
			s.logf("event[%d] input key=%q op=%s version=%d value=%s; current version=%d (%s); %d <= %d => IGNORE",
				i, e.Key, opName(e.Op), e.Version, formatValue(e.Value), current, source, e.Version, current)
			continue
		}

		s.applied++
		switch e.Op {
		case OpWrite:
			s.live[e.Key] = Row{Version: e.Version, Value: cloneBytes(e.Value)}
			delete(s.tombs, e.Key)
			s.logf("event[%d] input key=%q op=%s version=%d value=%s; current version=%d (%s); %d > %d => APPLY write (row upserted, tombstone cleared)",
				i, e.Key, opName(e.Op), e.Version, formatValue(e.Value), current, source, e.Version, current)
		case OpDelete:
			delete(s.live, e.Key)
			s.tombs[e.Key] = e.Version
			s.logf("event[%d] input key=%q op=%s version=%d; current version=%d (%s); %d > %d => APPLY delete (row removed, tombstone set)",
				i, e.Key, opName(e.Op), e.Version, current, source, e.Version, current)
		}
	}

	s.collectTombstonesLocked()
	s.logf("batch end: watermark=%d applied=%d ignored=%d; live rows: %s",
		s.watermark, s.applied, s.ignored, formatLive(s.live))
	return nil
}

// validate checks the whole batch before any state is touched.
func (s *Store) validate(events []*Event) error {
	if len(events) > s.maxBatch {
		return batchErrf(-1, ErrBatchTooLarge, "%d entries > limit %d", len(events), s.maxBatch)
	}
	seen := make(map[string]struct{}, len(events))
	for i, e := range events {
		if e == nil {
			return &BatchError{Index: i, Err: ErrNilEvent}
		}
		if e.Key == "" {
			return &BatchError{Index: i, Err: ErrEmptyKey}
		}
		if e.Version <= 0 {
			return batchErrf(i, ErrInvalidVersion, "got %d", e.Version)
		}
		switch e.Op {
		case OpWrite:
			if e.Value == nil {
				return &BatchError{Index: i, Err: ErrNilValue}
			}
		case OpDelete:
		default:
			return batchErrf(i, ErrInvalidOp, "got %d", e.Op)
		}
		if _, dup := seen[e.Key]; dup {
			return batchErrf(i, ErrDuplicateKey, "key %q", e.Key)
		}
		seen[e.Key] = struct{}{}
	}
	return nil
}

// currentVersionLocked returns the version of the live row, the tombstone,
// or zero when the key has no state, plus which source provided it.
func (s *Store) currentVersionLocked(key string) (int64, string) {
	if row, ok := s.live[key]; ok {
		return row.Version, "live row"
	}
	if tv, ok := s.tombs[key]; ok {
		return tv, "tombstone"
	}
	return 0, "no state"
}

// collectTombstonesLocked clears every tombstone whose age against the
// watermark has reached the retention parameter.
func (s *Store) collectTombstonesLocked() {
	for key, tv := range s.tombs {
		age := s.watermark - tv
		if age >= s.retention {
			delete(s.tombs, key)
			s.logf("tombstone GC: key=%q tombVersion=%d watermark=%d retention=%d; age %d >= %d => tombstone cleared, key back to no state",
				key, tv, s.watermark, s.retention, age, s.retention)
		}
	}
}

// Get returns the live row for key. A deleted (tombstoned) key misses.
func (s *Store) Get(key string) (Row, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.live[key]
	if ok {
		row.Value = cloneBytes(row.Value)
	}
	return row, ok
}

// TombstoneVersion reports whether a tombstone currently exists for key.
func (s *Store) TombstoneVersion(key string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tv, ok := s.tombs[key]
	return tv, ok
}

// Watermark returns the maximum event version seen in accepted batches.
func (s *Store) Watermark() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watermark
}

// Counts returns the cumulative applied and ignored event counts.
func (s *Store) Counts() (applied, ignored int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied, s.ignored
}

// Snapshot is a deep, point-in-time copy of store state.
type Snapshot struct {
	Live      map[string]Row
	Tombs     map[string]int64
	Watermark int64
	Applied   int64
	Ignored   int64
}

// Snapshot returns a deep copy safe to retain outside the lock.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{
		Live:      make(map[string]Row, len(s.live)),
		Tombs:     make(map[string]int64, len(s.tombs)),
		Watermark: s.watermark,
		Applied:   s.applied,
		Ignored:   s.ignored,
	}
	for key, row := range s.live {
		snap.Live[key] = Row{Version: row.Version, Value: cloneBytes(row.Value)}
	}
	for key, tv := range s.tombs {
		snap.Tombs[key] = tv
	}
	return snap
}

// SelfCheck verifies internal invariants. It is safe to run concurrently
// with Commit because it works off a read-locked snapshot.
func (s *Store) SelfCheck() error {
	snap := s.Snapshot()

	s.mu.RLock()
	retention := s.retention
	s.mu.RUnlock()

	var violations []string
	maxVersion := int64(0)
	for key, row := range snap.Live {
		if _, ok := snap.Tombs[key]; ok {
			violations = append(violations, fmt.Sprintf("key %q has both a live row and a tombstone", key))
		}
		if row.Version <= 0 {
			violations = append(violations, fmt.Sprintf("key %q has non-positive row version %d", key, row.Version))
		}
		if row.Version > maxVersion {
			maxVersion = row.Version
		}
	}
	for key, tv := range snap.Tombs {
		if tv <= 0 {
			violations = append(violations, fmt.Sprintf("key %q has non-positive tombstone version %d", key, tv))
		}
		if tv > maxVersion {
			maxVersion = tv
		}
		if snap.Watermark-tv >= retention {
			violations = append(violations, fmt.Sprintf("key %q tombstone age %d reached retention %d but was not cleared", key, snap.Watermark-tv, retention))
		}
	}
	if snap.Watermark < maxVersion {
		violations = append(violations, fmt.Sprintf("watermark %d is below maximum state version %d", snap.Watermark, maxVersion))
	}
	if len(violations) > 0 {
		return fmt.Errorf("versioned: self-check failed: %s", strings.Join(violations, "; "))
	}
	return nil
}

func (s *Store) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func opName(op Op) string {
	switch op {
	case OpWrite:
		return "write"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("unknown(%d)", op)
	}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func formatValue(v []byte) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", string(v))
}

func formatLive(live map[string]Row) string {
	if len(live) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(live))
	for key := range live {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s@%d=%q", key, live[key].Version, string(live[key].Value)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
