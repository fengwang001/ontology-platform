// Package api is the public entry point of the merge-on-read key-value store.
// It depends only on store.
package api

import (
	"errors"

	"ontology/store"
)

// Sentinel errors: the two failure modes are distinct and decidable.
var (
	ErrEmptyKey     = store.ErrEmptyKey
	ErrBadThreshold = store.ErrBadThreshold
)

// Store is the public merge-on-read key-value store.
type Store struct {
	st *store.Store
}

// New creates a Store with compaction threshold T (T must be positive).
func New(t int) (*Store, error) {
	st, err := store.New(t)
	if err != nil {
		return nil, err
	}
	return &Store{st: st}, nil
}

// Set writes k=v.
func (s *Store) Set(k, v string) error { return s.st.Set(k, v) }

// Del removes k.
func (s *Store) Del(k string) error { return s.st.Del(k) }

// Read returns the merged value of k; ok=false means absent.
func (s *Store) Read(k string) (v string, ok bool) { return s.st.Read(k) }

// BaseKeys returns the keys present in the compacted base, sorted.
func (s *Store) BaseKeys() []string { return s.st.BaseKeys() }

// DeltaLen returns the current delta length (always < T).
func (s *Store) DeltaLen() int { return s.st.DeltaLen() }

// SelfCheck replays the built-in T=4 sequence on a fresh store reached only
// through the public API, and verifies the four invariants against an
// independent naive recomputation. It does not touch the receiver's state, so
// concurrent SelfCheck calls on a shared store are safe.
func (s *Store) SelfCheck() error {
	st, err := New(4)
	if err != nil {
		return err
	}
	ops := []struct {
		del  bool
		k, v string
	}{
		{false, "a", "1"}, {false, "b", "2"}, {false, "c", "3"},
		{true, "c", ""}, {false, "b", "9"}, {false, "c", "7"}, {false, "c", "8"},
	}
	ref := map[string]string{} // independent naive merge on an empty base
	for i, op := range ops {
		if op.del {
			if err := st.Del(op.k); err != nil {
				return err
			}
			delete(ref, op.k)
		} else {
			if err := st.Set(op.k, op.v); err != nil {
				return err
			}
			ref[op.k] = op.v
		}
		if st.DeltaLen() > 3 { // invariant 3: read cost = len(delta) <= T-1
			return errors.New("selfcheck: delta exceeded T-1")
		}
		for _, k := range []string{"a", "b", "c", "absent"} {
			gv, gok := st.Read(k)
			wv, wok := ref[k]
			if gok != wok || (wok && gv != wv) { // invariant 1 & 2
				return errors.New("selfcheck: mismatch vs naive recompute")
			}
		}
		if i == 3 {
			if keys := st.BaseKeys(); len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
				return errors.New("selfcheck: tombstone left a trace in base")
			}
		}
	}
	if v, ok := st.Read("c"); !ok || v != "8" { // (乙): latest write wins
		return errors.New("selfcheck: c should be 8")
	}
	// Invariant 4: distinct sentinels, rejection leaves no trace.
	if _, e := New(-1); !errors.Is(e, ErrBadThreshold) {
		return errors.New("selfcheck: T<=0 not rejected")
	}
	before := st.DeltaLen()
	e1 := st.Set("", "z")
	e2 := st.Del("")
	if !errors.Is(e1, ErrEmptyKey) || !errors.Is(e2, ErrEmptyKey) ||
		errors.Is(e1, ErrBadThreshold) || st.DeltaLen() != before {
		return errors.New("selfcheck: empty-key rejection not clean")
	}
	return nil
}
