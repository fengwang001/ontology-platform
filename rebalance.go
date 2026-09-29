package rebalance

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
)

type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhaseMigrating Phase = "migrating"
)

type Logger interface {
	Printf(format string, args ...any)
}

type Store struct {
	mu         sync.RWMutex
	partitions []map[string]string
	n          int
	phase      Phase
	moveKeys   []string
	cursor     int
	target     int
	logf       func(string, ...any)
}

func New(partitions int, logger Logger) *Store {
	if partitions < 1 {
		panic(ErrInvalidPartitionCount)
	}
	s := &Store{
		partitions: make([]map[string]string, partitions),
		n:          partitions,
		phase:      PhaseIdle,
		target:     partitions,
	}
	for i := range s.partitions {
		s.partitions[i] = map[string]string{}
	}
	if logger != nil {
		s.logf = logger.Printf
	}
	s.log("NEW partitions=%d", partitions)
	return s
}

func (s *Store) hash(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

func (s *Store) homeOf(key string) int {
	return int(s.hash(key) % uint64(s.n))
}

// HomePartition reports the committed home partition hash(key) % N for the
// active partition count. It is the reference used to verify that every
// record is in its correct place after a rebalance.
func (s *Store) HomePartition(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.homeOf(key)
}

func (s *Store) targetHomeOf(key string) int {
	return int(s.hash(key) % uint64(s.target))
}

// locateLocked returns the partition that currently owns key.
// During a migration, keys whose migration-list index is below the
// cursor have already moved and are served from the target layout;
// all other keys are served from the old layout.
func (s *Store) locateLocked(key string) int {
	if s.phase != PhaseMigrating {
		return s.homeOf(key)
	}
	if _, moved := s.movedIndexLocked(key); moved {
		return s.targetHomeOf(key)
	}
	return s.homeOf(key)
}

// movedIndexLocked reports the key's index in the sorted migration
// list and whether that index has already been passed by the cursor.
func (s *Store) movedIndexLocked(key string) (int, bool) {
	idx := sort.SearchStrings(s.moveKeys, key)
	if idx < len(s.moveKeys) && s.moveKeys[idx] == key {
		return idx, idx < s.cursor
	}
	return -1, false
}

func (s *Store) existsLocked(key string) bool {
	for _, p := range s.partitions {
		if _, ok := p[key]; ok {
			return true
		}
	}
	return false
}

func (s *Store) Get(key string) (string, bool, error) {
	s.mu.RLock()
	pid := s.locateLocked(key)
	v, ok := s.partitions[pid][key]
	state := s.routingStateLocked(key)
	s.mu.RUnlock()
	s.log("GET key=%q -> partition=%d hit=%t (%s)", key, pid, ok, state)
	return v, ok, nil
}

func (s *Store) Put(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == PhaseMigrating && !s.existsLocked(key) {
		s.log("PUT key=%q REJECTED: %s | %s", key, ErrNewKeyWhileMigrating, s.debugDumpLocked("put-reject"))
		return ErrNewKeyWhileMigrating
	}
	pid := s.locateLocked(key)
	s.partitions[pid][key] = value
	state := s.routingStateLocked(key)
	s.log("PUT key=%q value=%q -> partition=%d (%s) | %s", key, value, pid, state, s.debugDumpLocked("put"))
	return nil
}

func (s *Store) Progress() (phase Phase, currentN, targetN, cursor, total int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	phase, currentN, targetN = s.phase, s.n, s.target
	cursor, total = s.cursor, len(s.moveKeys)
	return
}

func (s *Store) Partitions() []map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]string, len(s.partitions))
	for i, p := range s.partitions {
		cp := make(map[string]string, len(p))
		for k, v := range p {
			cp[k] = v
		}
		out[i] = cp
	}
	return out
}

func (s *Store) routingStateLocked(key string) string {
	oldHome := s.homeOf(key)
	if s.phase != PhaseMigrating {
		return fmt.Sprintf("phase=idle home=hash%%%d=%d", s.n, oldHome)
	}
	newHome := s.targetHomeOf(key)
	idx, moved := s.movedIndexLocked(key)
	return fmt.Sprintf("phase=migrating old=hash%%%d=%d new=hash%%%d=%d inList=%t index=%d cursor=%d moved=%t",
		s.n, oldHome, s.target, newHome, idx >= 0, idx, s.cursor, moved)
}

func (s *Store) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

func (s *Store) debugDump(tag string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.debugDumpLocked(tag)
}

func (s *Store) debugDumpLocked(tag string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tag=%s phase=%s n=%d target=%d cursor=%d/%d", tag, s.phase, s.n, s.target, s.cursor, len(s.moveKeys))
	for i, p := range s.partitions {
		keys := make([]string, 0, len(p))
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, " | P%d=%v", i, keys)
	}
	return b.String()
}
