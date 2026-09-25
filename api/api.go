// Package api is the public face of the append-log store.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/seglog"
)

// Sentinel errors, each individually distinguishable.
var (
	ErrEmptyKey       = errors.New("api: empty key")
	ErrRecordTooLarge = errors.New("api: record exceeds maxRec")
	ErrBadParams      = errors.New("api: invalid B/T/maxRec parameters")
)

// Store serializes access to the underlying seglog.Log.
type Store struct {
	mu     sync.RWMutex
	log    *seglog.Log
	maxRec int
}

// New validates parameters before creating anything.
func New(B, T, maxRec int) (*Store, error) {
	if B <= 0 || T <= 1 || maxRec <= 0 {
		return nil, ErrBadParams
	}
	return &Store{log: seglog.New(B, T), maxRec: maxRec}, nil
}

// Append validates first; a rejected append changes no state.
func (s *Store) Append(k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	if len(k)+len(v) > s.maxRec {
		return ErrRecordTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Append(k, v)
	return nil
}

// Get returns the newest value for k.
func (s *Store) Get(k string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.log.Get(k)
}

// LogicalBytes is the total logical bytes ever appended.
func (s *Store) LogicalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.log.LogicalBytes()
}

// PhysicalBytes is the total bytes written by flushes and merges.
func (s *Store) PhysicalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.log.PhysicalBytes()
}

// Amplification is PhysicalBytes/LogicalBytes (0 when empty).
func (s *Store) Amplification() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.log.LogicalBytes() == 0 {
		return 0
	}
	return float64(s.log.PhysicalBytes()) / float64(s.log.LogicalBytes())
}

// SelfCheck verifies the four invariants on fresh internal stores.
// It never touches the receiver's state, so it is race-safe.
func (s *Store) SelfCheck() error {
	// Invariants 2+3: the six-step trace pins exact physical bytes,
	// which only hold if flush is at >=B and merge is at >=T.
	st, err := New(6, 2, 1<<20)
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"a", "1"}, {"b", "1"}, {"c", "1"}, {"d", "12"}, {"a", "12"}, {"e", "1"},
	} {
		if err := st.Append(kv[0], kv[1]); err != nil {
			return err
		}
	}
	if st.LogicalBytes() != 14 || st.PhysicalBytes() != 22 {
		return fmt.Errorf("selfcheck: trace got %d/%d, want 14/22",
			st.LogicalBytes(), st.PhysicalBytes())
	}
	if got := st.Amplification(); got != 22.0/14.0 {
		return fmt.Errorf("selfcheck: amp %v, want %v", got, 22.0/14.0)
	}
	// Invariant 1: Get matches naive last-write-wins per key.
	st2, _ := New(4, 3, 1<<20)
	naive := map[string]string{}
	for i, kv := range [][2]string{
		{"x", "1"}, {"y", "2"}, {"x", "3"}, {"z", "4"}, {"y", "5"}, {"x", "6"},
	} {
		if err := st2.Append(kv[0], kv[1]); err != nil {
			return err
		}
		naive[kv[0]] = kv[1]
		for k, want := range naive {
			if got, ok := st2.Get(k); !ok || got != want {
				return fmt.Errorf("selfcheck: step %d Get(%q)=%q,%v want %q", i, k, got, ok, want)
			}
		}
	}
	// Invariant 4: rejected appends leave every counter unchanged.
	st3, _ := New(6, 2, 4)
	if err := st3.Append("ab", "12"); err != nil {
		return err
	}
	lo, ph := st3.LogicalBytes(), st3.PhysicalBytes()
	if err := st3.Append("", "v"); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("selfcheck: empty key err=%v", err)
	}
	if err := st3.Append("abc", "de"); !errors.Is(err, ErrRecordTooLarge) {
		return fmt.Errorf("selfcheck: oversize err=%v", err)
	}
	if _, err := New(0, 2, 4); !errors.Is(err, ErrBadParams) {
		return fmt.Errorf("selfcheck: bad params err=%v", err)
	}
	if st3.LogicalBytes() != lo || st3.PhysicalBytes() != ph {
		return errors.New("selfcheck: rejected append changed state")
	}
	return nil
}
