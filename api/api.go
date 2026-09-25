// Package api is the public face of the column-level LWW store.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/row"
)

// Rejectable, mutually distinct sentinel errors.
var (
	ErrEmptyKey   = errors.New("api: empty key")
	ErrEmptyCol   = errors.New("api: empty column")
	ErrNegativeTS = errors.New("api: negative timestamp")
	ErrEmptyVal   = errors.New("api: empty value (use Del)")
)

// Store is a concurrency-safe collection of per-key LWW rows.
type Store struct {
	mu        sync.RWMutex
	rows      map[string]*row.Row
	conflicts map[[2]string]bool
}

// New returns an empty store.
func New() *Store {
	return &Store{rows: make(map[string]*row.Row), conflicts: make(map[[2]string]bool)}
}

// Put writes a value and Del writes a tombstone; both validate before
// touching state, so rejected calls leave the store unchanged.
func (s *Store) Put(key, col string, ts int64, val string) error {
	if err := check(key, col, ts); err != nil {
		return err
	}
	if val == "" {
		return ErrEmptyVal
	}
	s.apply(key, col, ts, val, false)
	return nil
}

// Del tombstones one column.
func (s *Store) Del(key, col string, ts int64) error {
	if err := check(key, col, ts); err != nil {
		return err
	}
	s.apply(key, col, ts, "", true)
	return nil
}

func check(key, col string, ts int64) error {
	switch {
	case key == "":
		return ErrEmptyKey
	case col == "":
		return ErrEmptyCol
	case ts < 0:
		return ErrNegativeTS
	}
	return nil
}

func (s *Store) apply(key, col string, ts int64, val string, tomb bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[key]
	if r == nil {
		r = row.New()
		s.rows[key] = r
	}
	if r.Apply(col, ts, val, tomb) {
		s.conflicts[[2]string{key, col}] = true
	}
}

// View returns each key's live columns (deleted/never-written absent).
func (s *Store) View() map[string]map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]string, len(s.rows))
	for k, r := range s.rows {
		out[k] = r.View()
	}
	return out
}

// Conflicted returns the set of (Key, Col) pairs that saw a tie conflict.
func (s *Store) Conflicted() map[[2]string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[[2]string]bool, len(s.conflicts))
	for k, v := range s.conflicts {
		out[k] = v
	}
	return out
}

// SelfCheck replays built-in write sequences against a throwaway store
// and verifies the four invariants; it never touches the receiver.
func (s *Store) SelfCheck() error {
	t := New()
	seq := []struct {
		op       string
		key, col string
		ts       int64
		val      string
	}{
		{"put", "R", "c1", 5, "a"}, {"put", "R", "c2", 7, "x"},
		{"put", "R", "c1", 5, "b"}, {"put", "R", "c1", 9, "c"},
		{"del", "R", "c2", 8, ""}, {"put", "R", "c1", 4, "old"},
		{"put", "R", "c3", 6, "y"}, {"del", "R", "c3", 2, ""},
	}
	for _, st := range seq {
		var err error
		if st.op == "put" {
			err = t.Put(st.key, st.col, st.ts, st.val)
		} else {
			err = t.Del(st.key, st.col, st.ts)
		}
		if err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
	}
	// Invariants 1-3: final view equals the batch-recomputed winners.
	v := t.View()["R"]
	if len(v) != 2 || v["c1"] != "c" || v["c3"] != "y" {
		return fmt.Errorf("selfcheck view: got %v", v)
	}
	if !t.Conflicted()[[2]string{"R", "c1"}] || len(t.Conflicted()) != 1 {
		return fmt.Errorf("selfcheck conflicts: got %v", t.Conflicted())
	}
	// Invariant 4: rejected ops leave no trace.
	before := fmt.Sprint(t.View(), t.Conflicted())
	for _, err := range []error{
		t.Put("", "c", 1, "v"), t.Put("k", "", 1, "v"),
		t.Put("k", "c", -1, "v"), t.Put("k", "c", 1, ""),
		t.Del("", "c", 1), t.Del("k", "", 1), t.Del("k", "c", -1),
	} {
		if err == nil {
			return fmt.Errorf("selfcheck: rejection returned nil error")
		}
	}
	if fmt.Sprint(t.View(), t.Conflicted()) != before {
		return fmt.Errorf("selfcheck: rejected op mutated state")
	}
	return nil
}
