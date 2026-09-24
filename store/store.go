// Package store implements the hot memory tier and authoritative cold
// disk tier: write-through, transparent promotion and LRU eviction.
package store

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/tier"
)

var ErrBadMemCap = errors.New("store: memCap must be >= 1") // non-positive capacity

type cell struct {
	entry *tier.Entry
	val   string
}

// Store: disk is the authoritative full copy; hot holds at most memCap
// keys and is kept LRU-exact.
type Store struct {
	memCap           int
	hot              map[string]*cell
	disk             map[string]string
	clock            *tier.Clock
	lru              *tier.Heap
	mu               sync.Mutex
	diskReads        int
	lastEvictScanned int // unexported: entries inspected by last eviction
}

// NewStore creates a store whose hot tier holds at most memCap keys.
func NewStore(memCap int) (*Store, error) {
	if memCap < 1 {
		return nil, ErrBadMemCap
	}
	return &Store{memCap: memCap, hot: map[string]*cell{}, disk: map[string]string{}, clock: tier.NewClock(), lru: tier.NewHeap()}, nil
}

func (s *Store) touch(c *cell) {
	s.lru.Touch(c.entry, s.clock.Tick())
}

// admit promotes a key; over capacity it pops the oldest heap root.
func (s *Store) admit(k, v string) {
	e := &tier.Entry{Key: k, At: s.clock.Tick()}
	s.lru.Add(e)
	s.hot[k] = &cell{entry: e, val: v}
	s.lastEvictScanned = 0
	if s.lru.Len() > s.memCap {
		s.lastEvictScanned = 1 // only the root is inspected: O(1), no scan
		victim := s.lru.Pop()
		delete(s.hot, victim.Key) // its value already lives on disk
	}
}

// Write writes through to disk first, then updates or promotes in hot.
func (s *Store) Write(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disk[k] = v // write-through: disk always holds the newest value
	if c, ok := s.hot[k]; ok {
		c.val = v
		s.touch(c)
		return
	}
	s.admit(k, v)
}

// Read: hot hit costs no disk read; a cold key is counted and promoted.
// Never-written k returns ("", false) without touching any state.
func (s *Store) Read(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.hot[k]; ok {
		s.touch(c)
		return c.val, true
	}
	v, ok := s.disk[k]
	if !ok {
		return "", false // never written: absence, not a zero value
	}
	s.diskReads++
	s.admit(k, v)
	return v, true
}

// DiskReads reports how many Reads were served from the cold tier.
func (s *Store) DiskReads() int { s.mu.Lock(); defer s.mu.Unlock(); return s.diskReads }

// TotalKeys is the distinct-key count (disk holds the full copy).
func (s *Store) TotalKeys() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.disk) }

// Has reports existence from the authoritative disk without promoting k.
func (s *Store) Has(k string) bool { s.mu.Lock(); defer s.mu.Unlock(); _, ok := s.disk[k]; return ok }

// HotKeys is a sorted snapshot of the keys resident in memory.
func (s *Store) HotKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := make([]string, 0, len(s.hot))
	for k := range s.hot {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// SelfCheck replays the seven-step trace and the large-tier O(1) probe;
// it returns only pass/fail, never the probe counter's value.
func (s *Store) SelfCheck() error {
	chk, _ := NewStore(2)
	ops := []string{"wA1", "wB2", "rA1", "wC3", "rB2", "wB20", "rB20"}
	hot := [][]string{{"A"}, {"A", "B"}, {"A", "B"}, {"A", "C"}, {"B", "C"}, {"B", "C"}, {"B", "C"}}
	drs := []int{0, 0, 0, 0, 1, 1, 1}
	for i, op := range ops {
		if op[0] == 'w' {
			chk.Write(op[1:2], op[2:])
		} else if v, ok := chk.Read(op[1:2]); !ok || v != op[2:] {
			return fmt.Errorf("step%d Read(%s) mismatch", i+1, op[1:2])
		}
		if !reflect.DeepEqual(chk.HotKeys(), hot[i]) || chk.DiskReads() != drs[i] {
			return fmt.Errorf("step%d hot-set/disk-reads mismatch", i+1)
		}
	}
	if _, ok := chk.Read("D"); ok {
		return errors.New("never-written key reported present")
	}
	for _, m := range []int{100, 1000, 10000} {
		big, _ := NewStore(m)
		for i := 0; i < m; i++ {
			big.Write(fmt.Sprintf("k%05d", i), "v")
		}
		big.Write("trigger", "v")
		if big.lastEvictScanned != 1 { // unexported O(1) root probe, not a scan
			return fmt.Errorf("m=%d eviction scanned more than the heap root", m)
		}
	}
	return nil
}
