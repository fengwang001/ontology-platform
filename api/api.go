// Package api is the public surface of the tiered state store (depends on store).
package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/store"
)

// Distinct, decidable sentinel errors for every rejected operation.
var (
	ErrInvalidCapacity = errors.New("api: memCap must be positive")
	ErrEmptyKey        = errors.New("api: key must not be empty")
	ErrEmptyValue      = errors.New("api: value must not be empty")
	ErrKeyTooLong      = errors.New("api: key length exceeds maxKeyLen")
	ErrTooManyKeys     = errors.New("api: distinct key count would exceed maxKeys")
)

// Store is the tiered key/value store exposed to callers.
type Store struct {
	st        *store.Store
	maxKeyLen int
	maxKeys   int
}

// New builds a Store with a hot layer of memCap keys and validation limits.
func New(memCap, maxKeyLen, maxKeys int) (*Store, error) {
	if memCap <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Store{st: store.New(memCap), maxKeyLen: maxKeyLen, maxKeys: maxKeys}, nil
}

// Write validates before any mutation, so rejection leaves all state untouched.
func (s *Store) Write(k, v string) error {
	switch {
	case k == "":
		return ErrEmptyKey
	case v == "":
		return ErrEmptyValue
	case len(k) > s.maxKeyLen:
		return ErrKeyTooLong
	}
	if _, ok := s.st.ColdValue(k); !ok && s.st.Count() >= s.maxKeys {
		return ErrTooManyKeys
	}
	s.st.Write(k, v)
	return nil
}

// Read returns the latest value for k, or ("", false) if never written.
func (s *Store) Read(k string) (string, bool) { return s.st.Read(k) }

// DiskReads reports how many reads were served by the cold layer.
func (s *Store) DiskReads() int { return s.st.DiskReads() }

// snap renders all observable state for before/after rejection comparison.
func (s *Store) snap() string {
	var p []string
	for _, k := range s.st.HotKeys() {
		ts, _ := s.st.Stamp(k)
		cv, _ := s.st.ColdValue(k)
		p = append(p, fmt.Sprintf("%s@%d=%s", k, ts, cv))
	}
	sort.Strings(p)
	return strings.Join(p, " ") + fmt.Sprintf(" d=%d n=%d", s.st.DiskReads(), s.st.Count())
}

// newest returns the n largest-stamp keys (ties by key), sorted lexicographically.
func newest(st map[string]int64, n int) []string {
	ks := make([]string, 0, len(st))
	for k := range st {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(a, b int) bool {
		if st[ks[a]] != st[ks[b]] {
			return st[ks[a]] > st[ks[b]]
		}
		return ks[a] < ks[b]
	})
	if len(ks) > n {
		ks = ks[:n]
	}
	sort.Strings(ks)
	return ks
}

// SelfCheck replays the seven-op sequence and verifies all four invariants:
// replay consistency, write-through, exact LRU, and no-trace rejection.
func (s *Store) SelfCheck() error {
	t, err := New(2, 64, 32)
	if err != nil {
		return err
	}
	stamps := map[string]int64{}
	var clock int64
	touch := func(k string) { clock++; stamps[k] = clock }
	ops := [][3]string{ // {kind w/r, key, value}
		{"w", "A", "1"}, {"w", "B", "2"}, {"r", "A", ""},
		{"w", "C", "3"}, {"r", "B", ""}, {"w", "B", "20"}, {"r", "B", ""},
	}
	for i, o := range ops {
		if o[0] == "w" {
			if err := t.Write(o[1], o[2]); err != nil {
				return err
			}
		} else if _, ok := t.Read(o[1]); !ok {
			return fmt.Errorf("invariant 1: %s missing at op %d", o[1], i+1)
		}
		touch(o[1])
		if fmt.Sprint(newest(stamps, 2)) != fmt.Sprint(t.st.HotKeys()) {
			return fmt.Errorf("invariant 3 (exact LRU) failed after op %d", i+1)
		}
	}
	latest := map[string]string{"A": "1", "B": "20", "C": "3"}
	for k, v := range latest { // invariant 1 via Read (A is cold), 2 via disk
		if gv, ok := t.Read(k); !ok || gv != v {
			return fmt.Errorf("invariant 1 failed for %s: got %q,%v", k, gv, ok)
		}
		if cv, ok := t.st.ColdValue(k); !ok || cv != v {
			return fmt.Errorf("invariant 2 failed for %s: disk not latest", k)
		}
	}
	if _, ok := t.Read("D"); ok {
		return errors.New("invariant 1: unwritten key D reported found")
	}
	if _, err := New(0, 1, 1); !errors.Is(err, ErrInvalidCapacity) {
		return errors.New("invariant 4: non-positive memCap accepted")
	}
	bad, _ := New(2, 8, 3)
	for i, k := range []string{"a", "b", "c"} {
		if err := bad.Write(k, fmt.Sprint(i+1)); err != nil {
			return err
		}
	}
	before := bad.snap()
	rejects := [][2]string{{"", "x"}, {"k", ""}, {"toolongkey", "v"}, {"d", "v"}}
	for _, r := range rejects {
		if bad.Write(r[0], r[1]) == nil {
			return errors.New("invariant 4: an invalid write was accepted")
		}
	}
	if bad.snap() != before {
		return errors.New("invariant 4: rejected operation mutated state")
	}
	return nil
}
