// Package api is the public facade over the read-committed MVCC store.
// It depends only on mvcc (which depends on ver).
package api

import (
	"errors"
	"fmt"

	"ontology/mvcc"
)

// DB is an in-process key/value table with read-committed isolation.
type DB struct {
	s *mvcc.Store
}

// New returns an empty DB.
func New() *DB { return &DB{s: mvcc.New()} }

// Begin starts a transaction and returns its monotonic id.
func (d *DB) Begin() int { return d.s.Begin() }

// Write stages (k, v) into tx's pending set.
func (d *DB) Write(tx int, k, v string) error { return d.s.Write(tx, k, v) }

// Commit atomically publishes tx's pending writes.
func (d *DB) Commit(tx int) error { return d.s.Commit(tx) }

// Read returns the latest committed value of k; ok=false means absent.
func (d *DB) Read(k string) (string, bool) { return d.s.Read(k) }

// ReadTx reads within tx: own pending write wins, otherwise the current
// latest committed version is fetched (read-committed, no fixed snapshot).
func (d *DB) ReadTx(tx int, k string) (string, bool, error) {
	return d.s.ReadTx(tx, k)
}

// SelfCheck replays built-in operation sequences and verifies the four
// invariants: batch-reference equivalence, no dirty reads, own-write
// visibility, and rejection leaves no trace.
func (d *DB) SelfCheck() error {
	// Invariant 1: Read equals applying all committed txns in commit order.
	s := mvcc.New()
	ref := map[string]string{}
	put := func(tx int, k, v string) {
		if err := s.Write(tx, k, v); err != nil {
			panic(err)
		}
	}
	commit := func(tx int) {
		if err := s.Commit(tx); err != nil {
			panic(err)
		}
	}
	t1 := s.Begin()
	put(t1, "X", "1")
	commit(t1)
	ref["X"] = "1"
	t2 := s.Begin()
	put(t2, "X", "2")
	put(t2, "Y", "3")
	commit(t2)
	ref["X"], ref["Y"] = "2", "3"
	t3 := s.Begin()
	put(t3, "Y", "4")
	commit(t3)
	ref["Y"] = "4"
	for k, want := range ref {
		if got, ok := s.Read(k); !ok || got != want {
			return fmt.Errorf("self-check: batch equivalence failed for %q: got %q want %q", k, got, want)
		}
	}
	if _, ok := s.Read("missing"); ok {
		return errors.New("self-check: never-committed key reported present")
	}
	// Invariant 2: uncommitted writes are invisible to other readers.
	t4 := s.Begin()
	put(t4, "Z", "9")
	if _, ok := s.Read("Z"); ok {
		return errors.New("self-check: dirty read via Read")
	}
	if _, ok, err := s.ReadTx(s.Begin(), "Z"); err != nil || ok {
		return errors.New("self-check: dirty read via ReadTx")
	}
	// Invariant 3: a tx sees its own uncommitted write.
	if v, ok, err := s.ReadTx(t4, "Z"); err != nil || !ok || v != "9" {
		return fmt.Errorf("self-check: own write not visible (v=%q ok=%v err=%v)", v, ok, err)
	}
	// Invariant 4: rejected ops leave no trace and the store stays usable.
	bad := s.Write(t4, "", "x")
	if !errors.Is(bad, mvcc.ErrEmptyKey) {
		return fmt.Errorf("self-check: empty-key error wrong: %v", bad)
	}
	bad = s.Write(t4, "Q", "")
	if !errors.Is(bad, mvcc.ErrEmptyValue) {
		return fmt.Errorf("self-check: empty-value error wrong: %v", bad)
	}
	bad = s.Write(1<<30, "Q", "x")
	if !errors.Is(bad, mvcc.ErrTxNotBegun) {
		return fmt.Errorf("self-check: not-begun error wrong: %v", bad)
	}
	if _, ok, _ := s.ReadTx(t4, "Q"); ok {
		return errors.New("self-check: rejected write left a trace")
	}
	commit(t4) // pending set contains only Z=9; Z publishes now.
	bad = s.Commit(t4)
	if !errors.Is(bad, mvcc.ErrTxCommitted) {
		return fmt.Errorf("self-check: double-commit error wrong: %v", bad)
	}
	if v, ok := s.Read("Z"); !ok || v != "9" {
		return errors.New("self-check: store unusable after rejections")
	}
	return d.s.CheckReadCost() // O(1) read cost across several m values
}
