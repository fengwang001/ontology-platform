// Package api is the public facade over store: New, the KV operations and
// SelfCheck. It depends only on package store.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/store"
)

// Sentinel errors re-exported so callers only need to import api. All four are
// mutually distinct and decidable with errors.Is.
var (
	ErrInvalidLimit      = store.ErrInvalidLimit
	ErrEmptyKey          = store.ErrEmptyKey
	ErrLogFull           = store.ErrLogFull
	ErrSavepointNotFound = store.ErrSavepointNotFound
)

// KV is the nested-savepoint key/value store.
type KV struct{ s *store.Store }

// New creates a KV whose undo log holds at most undoLimit undo records.
func New(undoLimit int) (*KV, error) {
	s, err := store.New(undoLimit)
	if err != nil {
		return nil, err
	}
	return &KV{s}, nil
}

func (k *KV) Set(key string, value int) error { return k.s.Set(key, value) }
func (k *KV) Get(key string) (int, bool)      { return k.s.Get(key) }
func (k *KV) Savepoint() int                  { return k.s.Savepoint() }
func (k *KV) RollbackTo(id int) error         { return k.s.RollbackTo(id) }
func (k *KV) Release(id int) error            { return k.s.Release(id) }

// LocatedO1 reports the latest accepted rollback/release found its target
// savepoint by index rather than scanning the log. It exposes only a bool
// verdict, never the internal scan counter value.
func (k *KV) LocatedO1() bool { return k.s.LocatedO1() }

// testKeys is the fixed alphabet used by SelfCheck and tests.
var testKeys = []string{"a", "b", "c", "d", "e"}

// dump returns the current values of the test alphabet.
func dump(k *KV) map[string]int {
	m := map[string]int{}
	for _, c := range testKeys {
		if v, ok := k.Get(c); ok {
			m[c] = v
		}
	}
	return m
}

// eqMap reports keywise equality.
func eqMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ReleaseScenario exercises invariant 3: Release keeps every key, deactivates
// the target and deeper savepoints, and leaves the outer rollback intact.
func ReleaseScenario() error {
	c, _ := New(100)
	_ = c.Set("a", 1)
	s0 := c.Savepoint()
	_ = c.Set("b", 2)
	s1 := c.Savepoint()
	_ = c.Set("c", 3)
	keep := map[string]int{"a": 1, "b": 2, "c": 3}
	if e := c.Release(s1); e != nil || !eqMap(dump(c), keep) {
		return fmt.Errorf("release changed state: %v %v", e, dump(c))
	}
	for _, f := range []func(int) error{c.RollbackTo, c.Release} {
		if e := f(s1); !errors.Is(e, ErrSavepointNotFound) {
			return fmt.Errorf("released id still usable: %v", e)
		}
	}
	if e := c.RollbackTo(s0); e != nil || !eqMap(dump(c), map[string]int{"a": 1}) {
		return fmt.Errorf("outer rollback after release failed: %v %v", e, dump(c))
	}
	if e := c.RollbackTo(s0); !errors.Is(e, ErrSavepointNotFound) {
		return fmt.Errorf("consumed id reusable: %v", e)
	}
	return nil
}

// LocateScenario piles m undo records above one savepoint and rolls back,
// asserting the target was located by index (not by scanning the log).
func LocateScenario(m int) error {
	k, _ := New(1 << 20)
	sp := k.Savepoint()
	for i := 0; i < m; i++ {
		_ = k.Set("k", i)
	}
	if e := k.RollbackTo(sp); e != nil {
		return e
	}
	if !k.LocatedO1() {
		return fmt.Errorf("m=%d: target location scanned the log", m)
	}
	if _, ok := k.Get("k"); ok {
		return fmt.Errorf("m=%d: post-savepoint key survived rollback", m)
	}
	return nil
}

// ConcurrentScenario writes the test alphabet and has n goroutines read it,
// requiring keywise-identical results. Synchronization is via WaitGroup only.
func ConcurrentScenario(n int) error {
	k, _ := New(1000)
	want := map[string]int{}
	for i, key := range testKeys {
		_ = k.Set(key, i+1)
		want[key] = i + 1
	}
	var wg sync.WaitGroup
	res := make([]map[string]int, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g] = dump(k) }(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if !eqMap(res[g], want) {
			return fmt.Errorf("goroutine %d got %v want %v", g, res[g], want)
		}
	}
	return nil
}
