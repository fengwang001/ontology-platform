// Package ontology implements a version-vector based causal store with
// stable-vector driven tombstone reclamation.
package ontology

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Distinct, decidable validation errors.
var (
	// ErrInvalidReplicas is reported when the store is created with a
	// non-positive replica count.
	ErrInvalidReplicas = errors.New("ontology: replica count must be positive")
	// ErrReplicaOutOfRange is reported when an event names a replica id
	// outside [0, replicas).
	ErrReplicaOutOfRange = errors.New("ontology: replica id out of range")
	// ErrNegativeVector is reported when an event vector carries a
	// negative component.
	ErrNegativeVector = errors.New("ontology: version vector has a negative component")
	// ErrEmptyKey is reported when an event names the empty key.
	ErrEmptyKey = errors.New("ontology: key must not be empty")
	// ErrVectorLength is reported when an event vector length does not
	// match the store replica count.
	ErrVectorLength = errors.New("ontology: version vector length mismatch")
)

// Event is a single write or delete carried by a batch.
type Event struct {
	Key     string
	Replica int
	Vector  []int64
	Value   string
	Delete  bool
}

// Entry is the current winning record for a key.
type Entry struct {
	Vector []int64
	Value  string
	Tomb   bool
}

// Snapshot is an immutable point-in-time view of the store.
type Snapshot struct {
	Replicas      int
	Clocks        [][]int64
	Stable        []int64
	Entries       map[string]Entry
	DeletedGuards map[string][]int64
}

// Store is a concurrency-safe causal key-value store.
type Store struct {
	mu       sync.RWMutex
	replicas int
	clocks   [][]int64
	entries  map[string]Entry
	reaped   []string
	guards   map[string][]int64
	log      io.Writer
	logMu    sync.Mutex
}

// New creates a Store for replicasPerStore replicas. A nil logWriter disables
// logging. A non-positive replica count is rejected with ErrInvalidReplicas.
func New(replicasPerStore int, logWriter io.Writer) (*Store, error) {
	if replicasPerStore <= 0 {
		return nil, ErrInvalidReplicas
	}
	clocks := make([][]int64, replicasPerStore)
	for i := range clocks {
		clocks[i] = make([]int64, replicasPerStore)
	}
	return &Store{
		replicas: replicasPerStore,
		clocks:   clocks,
		entries:  make(map[string]Entry),
		guards:   make(map[string][]int64),
		log:      logWriter,
	}, nil
}

// Put is a one-event convenience wrapper around Apply for a value write.
func (s *Store) Put(key string, replica int, vector []int64, value string) error {
	return s.Apply([]Event{{Key: key, Replica: replica, Vector: vector, Value: value}})
}

// Delete is a one-event convenience wrapper around Apply for a tombstone.
func (s *Store) Delete(key string, replica int, vector []int64) error {
	return s.Apply([]Event{{Key: key, Replica: replica, Vector: vector, Delete: true}})
}

// Apply validates the whole batch and applies it atomically: if any event is
// invalid, nothing (including replica clocks) changes. Valid events are
// delivered in order. An event whose vector is not lexicographically greater
// than the current winner's vector is ignored for storage, but the replica
// clock still advances with it (the replica produced or caught up to it).
func (s *Store) Apply(events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range events {
		if err := s.validateEvent(&events[i]); err != nil {
			s.logf("apply rejected: batch=%d events, event[%d]=%s, err=%v; state unchanged",
				len(events), i, formatEvent(&events[i]), err)
			return err
		}
	}

	applied, ignored := 0, 0
	for i := range events {
		e := &events[i]
		componentWiseMax(s.clocks[e.Replica], e.Vector)
		if guard, gone := s.guards[e.Key]; gone && !lexicographicallyGreater(e.Vector, guard) {
			ignored++
			s.logf("event ignored (older than reclaimed tombstone): %s guard=%s; clock[%d] advanced anyway",
				formatEvent(e), vectorString(guard), e.Replica)
			continue
		}
		cur, ok := s.entries[e.Key]
		if ok && !lexicographicallyGreater(e.Vector, cur.Vector) {
			ignored++
			s.logf("event ignored (stale/duplicate): %s current=%s; clock[%d] advanced anyway",
				formatEvent(e), formatEntry(cur), e.Replica)
			continue
		}
		s.entries[e.Key] = Entry{Vector: cloneVector(e.Vector), Value: e.Value, Tomb: e.Delete}
		delete(s.guards, e.Key)
		applied++
		s.logf("event accepted: %s -> %s", formatEvent(e), formatEntry(s.entries[e.Key]))
	}
	s.logf("apply committed: applied=%d ignored=%d stable=%v", applied, ignored, s.stableLocked())
	return nil
}

func (s *Store) validateEvent(e *Event) error {
	if e.Replica < 0 || e.Replica >= s.replicas {
		return ErrReplicaOutOfRange
	}
	for _, x := range e.Vector {
		if x < 0 {
			return ErrNegativeVector
		}
	}
	if e.Key == "" {
		return ErrEmptyKey
	}
	if len(e.Vector) != s.replicas {
		return ErrVectorLength
	}
	return nil
}

// StableVector returns the component-wise minimum over the per-replica
// highest-seen clocks. It is monotonic: clocks only advance, so the returned
// vector can only grow component by component.
func (s *Store) StableVector() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stable := s.stableLocked()
	s.logf("stable vector queried: %v", stable)
	return stable
}

func (s *Store) stableLocked() []int64 {
	stable := cloneVector(s.clocks[0])
	for r := 1; r < s.replicas; r++ {
		for c := 0; c < s.replicas; c++ {
			if s.clocks[r][c] < stable[c] {
				stable[c] = s.clocks[r][c]
			}
		}
	}
	return stable
}

// Reclaim removes every tombstone entry whose vector is component-wise no
// greater than the current stable vector. Removed keys are returned in
// lexicographic order and appended to the reclaimed-key history. Live entries
// and tombstones still past the stability frontier are kept.
func (s *Store) Reclaim() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	stable := s.stableLocked()
	var removed []string
	for key, entry := range s.entries {
		if entry.Tomb && dominatedBy(entry.Vector, stable) {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	for _, key := range removed {
		vec := s.entries[key].Vector
		delete(s.entries, key)
		if guard, ok := s.guards[key]; !ok || lexicographicallyGreater(vec, guard) {
			s.guards[key] = vec
		}
		s.reaped = append(s.reaped, key)
		s.logf("tombstone reclaimed: key=%q vector=%s <= stable=%s", key, vectorString(vec), vectorString(stable))
	}
	sort.Strings(s.reaped)
	s.logf("reclaim complete: removed=%v stable=%v remaining=%d", removed, stable, len(s.entries))
	return removed
}

// View returns a deep-copied point-in-time snapshot, safe for callers to
// mutate. Concurrent read-only calls against a quiescent store return
// field-identical snapshots.
func (s *Store) View() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clocks := make([][]int64, s.replicas)
	for i := range clocks {
		clocks[i] = cloneVector(s.clocks[i])
	}
	entries := make(map[string]Entry, len(s.entries))
	for k, e := range s.entries {
		entries[k] = Entry{Vector: cloneVector(e.Vector), Value: e.Value, Tomb: e.Tomb}
	}
	guards := make(map[string][]int64, len(s.guards))
	for k, v := range s.guards {
		guards[k] = cloneVector(v)
	}
	snap := Snapshot{
		Replicas:      s.replicas,
		Clocks:        clocks,
		Stable:        s.stableLocked(),
		Entries:       entries,
		DeletedGuards: guards,
	}
	s.logf("view taken: entries=%d stable=%v", len(entries), snap.Stable)
	return snap
}

// ReapedKeys returns a sorted copy of every key ever removed by Reclaim.
func (s *Store) ReapedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStrings(s.reaped)
}

// SelfCheck verifies the internal invariants:
//   - all clocks and entry vectors have the configured length, no negatives;
//   - the stable vector equals the component-wise minimum of clocks;
//   - each stored entry vector is dominated by the component-wise maximum over
//     all replica clocks (every accepted vector was seen by some clock);
//   - the reclaimed list is sorted and duplicates-free.
func (s *Store) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for r := 0; r < s.replicas; r++ {
		if len(s.clocks[r]) != s.replicas {
			return errors.New("ontology: clock length mismatch")
		}
		for _, x := range s.clocks[r] {
			if x < 0 {
				return errors.New("ontology: negative clock component")
			}
		}
	}
	stable := s.stableLocked()
	ceiling := make([]int64, s.replicas)
	for r := 0; r < s.replicas; r++ {
		componentWiseMax(ceiling, s.clocks[r])
	}
	for key, entry := range s.entries {
		if len(entry.Vector) != s.replicas {
			return errors.New("ontology: entry vector length mismatch for key " + key)
		}
		if !dominatedBy(entry.Vector, ceiling) {
			return errors.New("ontology: entry vector exceeds known clocks for key " + key)
		}
	}
	for key, guard := range s.guards {
		if len(guard) != s.replicas {
			return errors.New("ontology: guard vector length mismatch for key " + key)
		}
		if !dominatedBy(guard, ceiling) {
			return errors.New("ontology: guard vector exceeds known clocks for key " + key)
		}
		if entry, ok := s.entries[key]; ok && entry.Tomb {
			return errors.New("ontology: guard coexists with live tombstone entry for key " + key)
		}
	}
	for i := 1; i < len(s.reaped); i++ {
		if s.reaped[i-1] >= s.reaped[i] {
			return errors.New("ontology: reclaimed key list not strictly sorted")
		}
	}
	s.logf("self-check ok: replicas=%d entries=%d stable=%v reaped=%v",
		s.replicas, len(s.entries), stable, s.reaped)
	return nil
}

func (s *Store) logf(format string, args ...any) {
	if s.log != nil {
		s.logMu.Lock()
		defer s.logMu.Unlock()
		logger := log.New(s.log, "", log.LstdFlags|log.Lmicroseconds)
		logger.Output(2, fmt.Sprintf(format, args...))
	}
}

func formatEvent(e *Event) string {
	kind := "put"
	if e.Delete {
		kind = "delete"
	}
	return fmt.Sprintf("%s{key=%s,replica=%d,vector=%s,value=%s}",
		kind, strconv.Quote(e.Key), e.Replica, vectorString(e.Vector), strconv.Quote(e.Value))
}

func formatEntry(e Entry) string {
	if e.Tomb {
		return "tomb{vector=" + vectorString(e.Vector) + "}"
	}
	return "value{vector=" + vectorString(e.Vector) + ",value=" + strconv.Quote(e.Value) + "}"
}

func vectorString(v []int64) string {
	parts := make([]string, 0, len(v))
	for _, x := range v {
		parts = append(parts, strconv.FormatInt(x, 10))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneVector(v []int64) []int64 {
	out := make([]int64, len(v))
	copy(out, v)
	return out
}

func sortedKeys(m map[string]Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
