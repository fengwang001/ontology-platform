// Package api is the public hot/cold tiered Key(string)->Value(int64) store.
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/store"
)

// Four pairwise-distinct sentinel errors, all errors.Is-matchable.
var (
	ErrBadCap, ErrEmptyKey, ErrNotFound, ErrColdFull = store.ErrBadCap, store.ErrEmptyKey, store.ErrNotFound, store.ErrColdFull
)

type Store struct{ s *store.Store }

func New(hotCap, maxCold int) (*Store, error) {
	s, e := store.New(hotCap, maxCold)
	if e != nil {
		return nil, e
	}
	return &Store{s}, nil
}

func (s *Store) Put(key string, val int64) error { return s.s.Put(key, val) }
func (s *Store) Get(key string) (int64, error)   { return s.s.Get(key) }
func (s *Store) Value(key string) (int64, bool)  { return s.s.Value(key) }
func (s *Store) HotKeys() []string               { return s.s.HotKeys() }
func (s *Store) ColdKeys() []string              { return s.s.ColdKeys() }

// scTrace is the NOTES.md eight-step script (cap 2/100); hot MRU->LRU,
// cold sorted, "" empty, ret for Get only.
var scTrace = []struct {
	put       bool
	k         string
	v, ret    int64
	hot, cold string
}{
	{true, "A", 1, 0, "A", ""}, {true, "B", 2, 0, "BA", ""},
	{false, "A", 0, 1, "AB", ""}, {true, "C", 3, 0, "CA", "B"},
	{false, "B", 0, 2, "BC", "A"}, {true, "D", 4, 0, "DB", "AC"},
	{false, "A", 0, 1, "AD", "BC"}, {true, "E", 5, 0, "EA", "BCD"},
}

// SelfCheck replays a built-in script and verifies the four invariants
// after every step (naive reference, tier consistency, exact MRU->LRU
// order, no-trace rejection), and checks the receiver's current state.
func (s *Store) SelfCheck() error {
	if err := s.consistent(s.HotKeys(), s.ColdKeys()); err != nil {
		return err
	}
	t, _ := New(2, 100)
	naive := map[string]int64{}
	for i, st := range scTrace {
		if st.put {
			if err := t.Put(st.k, st.v); err != nil {
				return err
			}
			naive[st.k] = st.v
		} else if v, e := t.Get(st.k); e != nil || v != st.ret {
			return fmt.Errorf("step %d: Get=%d %v want %d", i+1, v, e, st.ret)
		}
		hot, cold := t.HotKeys(), t.ColdKeys()
		if strings.Join(hot, "") != st.hot || strings.Join(cold, "") != st.cold { // inv 3
			return fmt.Errorf("step %d: hot=%v cold=%v want %q %q", i+1, hot, cold, st.hot, st.cold)
		}
		for k, want := range naive { // inv 1: ordinary-map reference
			if got, ok := t.Value(k); !ok || got != want {
				return fmt.Errorf("step %d: %s=%d ok=%v want %d", i+1, k, got, ok, want)
			}
		}
		if err := t.consistent(hot, cold); err != nil { // inv 2
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return t.checkRejections()
}

// consistent checks invariant 2 via the public API: every tier key has
// a value, tiers are disjoint, and cold keys are sorted ascending.
func (s *Store) consistent(hot, cold []string) error {
	seen := map[string]bool{}
	for _, k := range hot {
		if seen[k] {
			return fmt.Errorf("dup hot key %s", k)
		}
		seen[k] = true
		if _, ok := s.Value(k); !ok {
			return fmt.Errorf("hot key %s has no value", k)
		}
	}
	for i, k := range cold {
		if seen[k] {
			return fmt.Errorf("key %s in both tiers", k)
		}
		if i > 0 && cold[i-1] >= k {
			return fmt.Errorf("cold not sorted: %v", cold)
		}
		if _, ok := s.Value(k); !ok {
			return fmt.Errorf("cold key %s has no value", k)
		}
	}
	return nil
}

// checkRejections checks invariant 4: distinct sentinels, no trace, still usable.
func (s *Store) checkRejections() error {
	if _, e := New(0, 1); !errors.Is(e, ErrBadCap) {
		return fmt.Errorf("want ErrBadCap, got %v", e)
	}
	r, _ := New(1, 1)
	check := func(want error, f func() error) error {
		before := snap(r)
		e := f()
		if after := snap(r); !errors.Is(e, want) || before != after {
			return fmt.Errorf("want %v got %v; %q->%q", want, e, before, after)
		}
		return nil
	}
	if e := check(ErrEmptyKey, func() error { return r.Put("", 1) }); e != nil {
		return e
	}
	if e := check(ErrNotFound, func() error { _, ge := r.Get("z"); return ge }); e != nil {
		return e
	}
	_ = r.Put("a", 1)
	_ = r.Put("b", 2) // evict a into cold
	if e := check(ErrColdFull, func() error { return r.Put("c", 3) }); e != nil {
		return e
	}
	if v, _ := r.Get("a"); v != 1 { // still usable; value retained
		return fmt.Errorf("after rejection Get(a)=%d want 1", v)
	}
	if ErrBadCap == ErrEmptyKey || ErrBadCap == ErrNotFound || ErrBadCap == ErrColdFull ||
		ErrEmptyKey == ErrNotFound || ErrEmptyKey == ErrColdFull || ErrNotFound == ErrColdFull {
		return errors.New("the four sentinel errors are not distinct")
	}
	return nil
}

func snap(s *Store) string {
	var b strings.Builder
	for _, k := range append(s.HotKeys(), s.ColdKeys()...) {
		v, _ := s.Value(k)
		fmt.Fprintf(&b, "%s=%d;", k, v)
	}
	return b.String()
}
