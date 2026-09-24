// Package api is the outward-facing facade over package mvcc.
package api

import (
	"errors"
	"fmt"

	"ontology/mvcc"
)

// Re-exported sentinel errors so callers can distinguish rejections.
var (
	ErrEmptyKey    = mvcc.ErrEmptyKey
	ErrEmptyValue  = mvcc.ErrEmptyValue
	ErrTxNotBegun  = mvcc.ErrTxNotBegun
	ErrTxCommitted = mvcc.ErrTxCommitted
	ErrKeyNotFound = mvcc.ErrKeyNotFound
)

// DB is a read-committed MVCC key-value table.
type DB struct{ s *mvcc.Store }

// New returns an empty DB.
func New() *DB { return &DB{s: mvcc.New()} }

// Begin starts a transaction and returns its monotonically increasing id.
func (d *DB) Begin() int { return d.s.Begin() }

// Write buffers (k, v) in tx's pending set.
func (d *DB) Write(tx int, k, v string) error { return d.s.Write(tx, k, v) }

// Commit atomically publishes all of tx's pending writes.
func (d *DB) Commit(tx int) error { return d.s.Commit(tx) }

// Read returns the latest committed value of k.
func (d *DB) Read(k string) (string, error) { return d.s.Read(k) }

// ReadTx reads k under read-committed isolation within tx.
func (d *DB) ReadTx(tx int, k string) (string, error) { return d.s.ReadTx(tx, k) }

// SelfCheck runs built-in operation sequences verifying the four
// invariants plus the O(1) read-cost bound. It returns the first failure.
func (d *DB) SelfCheck() error {
	s := mvcc.New()

	// Invariant 1: Read matches the batch reference (apply committed
	// (k,v) pairs in commit order; final value must equal Read).
	ref := map[string]string{}
	for i := 0; i < 50; i++ {
		tx := s.Begin()
		k := fmt.Sprintf("k%d", i%7)
		v := fmt.Sprintf("v%d", i)
		if err := s.Write(tx, k, v); err != nil {
			return err
		}
		if err := s.Commit(tx); err != nil {
			return err
		}
		ref[k] = v
	}
	for k, want := range ref {
		got, err := s.Read(k)
		if err != nil || got != want {
			return fmt.Errorf("api: batch reference mismatch on %q", k)
		}
	}
	if _, err := s.Read("never-committed"); !errors.Is(err, mvcc.ErrKeyNotFound) {
		return fmt.Errorf("api: uncommitted key must read as not-found")
	}

	// Invariant 2: uncommitted writes are invisible to others.
	ta, tb := s.Begin(), s.Begin()
	if err := s.Write(ta, "ghost", "g"); err != nil {
		return err
	}
	if _, err := s.Read("ghost"); !errors.Is(err, mvcc.ErrKeyNotFound) {
		return fmt.Errorf("api: dirty read via Read")
	}
	if _, err := s.ReadTx(tb, "ghost"); !errors.Is(err, mvcc.ErrKeyNotFound) {
		return fmt.Errorf("api: dirty read via ReadTx")
	}

	// Invariant 3: a transaction sees its own uncommitted writes.
	if v, err := s.ReadTx(ta, "ghost"); err != nil || v != "g" {
		return fmt.Errorf("api: own write not visible")
	}

	// Invariant 4: rejected operations leave no trace and the store
	// stays usable.
	before, err := s.Read("k0")
	if err != nil {
		return err
	}
	for _, e := range []error{
		s.Write(tb, "", "x"), s.Write(tb, "k", ""),
		s.Write(1<<30, "k", "v"), s.Commit(1 << 30),
		s.Write(ta, "", ""), // ta still active: empty key rejected
	} {
		if e == nil {
			return fmt.Errorf("api: invalid operation accepted")
		}
	}
	if v, _ := s.Read("k0"); v != before {
		return fmt.Errorf("api: rejected op changed committed state")
	}
	if err := s.Commit(ta); err != nil { // ta still usable
		return fmt.Errorf("api: store unusable after rejections: %w", err)
	}
	if v, _ := s.Read("ghost"); v != "g" {
		return fmt.Errorf("api: commit after rejections lost writes")
	}

	// O(1) read cost: inspected versions must not grow with history.
	if err := s.CheckReadCost(1000); err != nil {
		return err
	}
	return nil
}
