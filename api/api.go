// Package api is the public facade over the merge-on-read store.
// It depends only on store. The merge-cost counter is never exposed here.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/store"
)

// Distinct, decidable sentinel errors re-exported from store.
var (
	ErrEmptyKey         = store.ErrEmptyKey
	ErrInvalidThreshold = store.ErrBadThreshold
)

// Store is the public merge-on-read key/value store.
type Store struct{ s *store.Store }

// New creates a store with positive compaction threshold T.
func New(T int) (*Store, error) {
	st, err := store.New(T)
	if err != nil {
		return nil, err
	}
	return &Store{s: st}, nil
}

func (s *Store) Set(k, v string) error        { return s.s.Set(k, v) }
func (s *Store) Del(k string) error           { return s.s.Del(k) }
func (s *Store) Read(k string) (string, bool) { return s.s.Read(k) }
func (s *Store) BaseKeys() []string           { return s.s.BaseKeys() }
func (s *Store) DeltaLen() int                { return s.s.DeltaLen() }

// SelfCheck replays a built-in sequence and verifies the four invariants.
// The private cost counter is never read; boundedness is seen via DeltaLen.
func (s *Store) SelfCheck() error {
	const T = 4
	st, err := store.New(T)
	if err != nil {
		return err
	}
	naive := map[string]string{} // plain recompute: absent key is simply absent
	step := func(k, v string, del bool) error {
		if del {
			if err := st.Del(k); err != nil {
				return err
			}
			delete(naive, k)
		} else {
			if err := st.Set(k, v); err != nil {
				return err
			}
			naive[k] = v
		}
		if st.DeltaLen() > T-1 { // invariant 3
			return fmt.Errorf("invariant 3: delta len %d >= T", st.DeltaLen())
		}
		return nil
	}
	// Seven-step sequence plus a delete-without-rewrite tombstone probe.
	seq := []struct {
		k, v string
		del  bool
	}{
		{"a", "1", false}, {"b", "2", false}, {"c", "3", false}, {"c", "", true},
		{"b", "9", false}, {"c", "7", false}, {"c", "8", false}, {"gone", "x", false}, {"gone", "", true},
	}
	for _, e := range seq {
		if err := step(e.k, e.v, e.del); err != nil {
			return err
		}
	}
	for k, want := range naive { // invariants 1 & 2: key-by-key equal to naive recompute
		if got, ok := st.Read(k); !ok || got != want {
			return fmt.Errorf("invariant 1/2: %q got %q,%v want %q", k, got, ok, want)
		}
	}
	if _, ok := st.Read("gone"); ok { // invariant 2: tombstone, no rewrite
		return errors.New("invariant 2: deleted key reads as present")
	}
	if err := checkRejections(st); err != nil { // invariant 4
		return err
	}
	big, _ := store.New(T) // invariant 3 at scale
	for i := 0; i < 1000; i++ {
		_ = big.Set(fmt.Sprintf("k%d", i), "x")
	}
	if big.DeltaLen() >= T {
		return fmt.Errorf("invariant 3 after 1000 sets: delta len %d", big.DeltaLen())
	}
	return nil
}

func checkRejections(st *store.Store) error {
	dl, bk := st.DeltaLen(), st.BaseKeys()
	v, ok := st.Read("a")
	if err := st.Set("", "x"); !errors.Is(err, store.ErrEmptyKey) {
		return fmt.Errorf("invariant 4: Set empty key err=%v", err)
	}
	if err := st.Del(""); !errors.Is(err, store.ErrEmptyKey) {
		return fmt.Errorf("invariant 4: Del empty key err=%v", err)
	}
	if st.DeltaLen() != dl || !reflect.DeepEqual(st.BaseKeys(), bk) {
		return errors.New("invariant 4: state changed after rejected op")
	}
	if g, o := st.Read("a"); g != v || o != ok {
		return errors.New("invariant 4: read changed after rejected op")
	}
	if _, err := store.New(0); !errors.Is(err, store.ErrBadThreshold) {
		return fmt.Errorf("invariant 4: New(0) err=%v", err)
	}
	if _, err := store.New(-3); !errors.Is(err, store.ErrBadThreshold) {
		return fmt.Errorf("invariant 4: New(-3) err=%v", err)
	}
	if store.ErrEmptyKey == store.ErrBadThreshold {
		return errors.New("invariant 4: the two sentinel errors must differ")
	}
	return nil
}
