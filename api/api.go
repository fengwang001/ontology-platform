// Package api is the public face of the SSI key-value engine.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/ssi"
)

// Distinguishable sentinel errors.
var (
	ErrNoActiveTxn = ssi.ErrNoActiveTxn
	ErrEmptyKey    = ssi.ErrEmptyKey
	ErrTxnEnded    = ssi.ErrTxnEnded
	ErrConflict    = ssi.ErrConflict
)

// DB is the engine handle.
type DB struct{ m *ssi.Manager }

// New returns an empty engine.
func New() *DB { return &DB{m: ssi.New()} }

// Begin starts a transaction on the current snapshot; Committed copies the committed state.
func (d *DB) Begin() *Txn                  { return &Txn{t: d.m.Begin()} }
func (d *DB) Committed() map[string]string { return d.m.Committed() }

// Txn is one in-flight transaction; the zero value is unusable.
type Txn struct{ t *ssi.Txn }

// Read returns own buffered write if present, else the snapshot value.
func (tx *Txn) Read(key string) (string, error) { return tx.t.Read(key) }

// Write buffers val under key; Commit detects rw conflicts and rolls back on write skew.
func (tx *Txn) Write(key, val string) error { return tx.t.Write(key, val) }
func (tx *Txn) Commit() error               { return tx.t.Commit() }

// SelfCheck verifies the four invariants on built-in transaction sequences.
func (d *DB) SelfCheck() error {
	for _, c := range []struct {
		name string
		fn   func() error
	}{{"serial+skew", checkSerialSkew}, {"no-trace", checkNoTrace}} {
		if err := c.fn(); err != nil {
			return fmt.Errorf("selfcheck %s: %w", c.name, err)
		}
	}
	return nil
}

func seq(steps ...func() error) error {
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

func seed(db *DB, init map[string]string) error {
	t := db.Begin()
	for k, v := range init {
		_ = t.Write(k, v) // fresh txn, non-empty keys: cannot fail
	}
	return t.Commit()
}

func eqState(got, want map[string]string) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("state %v != want %v", got, want)
	}
	return nil
}

// checkSerialSkew runs the 14-step scenario: T2 must roll back (invariant 2),
// T3 reads its own write (invariant 3), and the final state must equal the
// commit-order serial replay of committed transactions (invariant 1).
func checkSerialSkew() error {
	db := New()
	var t1, t2, t3 *Txn
	err := seq(
		func() error { return seed(db, map[string]string{"x": "10", "y": "10", "z": "5"}) },
		func() error { t1, t2 = db.Begin(), db.Begin(); return nil },
		func() error { _, e := t1.Read("x"); return e },
		func() error { _, e := t1.Read("y"); return e },
		func() error { _, e := t2.Read("x"); return e },
		func() error { _, e := t2.Read("y"); return e },
		func() error { t3 = db.Begin(); return nil },
		func() error { return t3.Write("z", "99") },
		func() error {
			v, e := t3.Read("z")
			if e == nil && v != "99" {
				e = fmt.Errorf("read-own-write = %q, want 99", v)
			}
			return e
		},
		func() error { return t1.Write("x", "-10") },
		func() error { return t1.Commit() },
		func() error { return t3.Commit() },
		func() error { return t2.Write("y", "-10") },
	)
	if err != nil {
		return err
	}
	if err := t2.Commit(); !errors.Is(err, ErrConflict) {
		return fmt.Errorf("T2 should roll back, got %v", err)
	}
	// Serial replay in commit order: seed, T1, T3 (T2 rolled back).
	return eqState(db.Committed(), map[string]string{"x": "-10", "y": "10", "z": "99"})
}

// checkNoTrace: rejected operations are distinguishable and change nothing
// (invariant 4).
func checkNoTrace() error {
	db := New()
	if err := seed(db, map[string]string{"a": "1"}); err != nil {
		return err
	}
	before := db.Committed()
	var zero Txn
	t := db.Begin()
	ops := []struct {
		name string
		run  func() error
		want error
	}{
		{"zeroRead", func() error { _, e := zero.Read("a"); return e }, ErrNoActiveTxn},
		{"zeroWrite", func() error { return zero.Write("a", "9") }, ErrNoActiveTxn},
		{"zeroCommit", zero.Commit, ErrNoActiveTxn},
		{"emptyRead", func() error { _, e := t.Read(""); return e }, ErrEmptyKey},
		{"emptyWrite", func() error { return t.Write("", "9") }, ErrEmptyKey},
		{"commit", t.Commit, nil},
		{"endedRead", func() error { _, e := t.Read("a"); return e }, ErrTxnEnded},
		{"endedWrite", func() error { return t.Write("a", "9") }, ErrTxnEnded},
		{"endedCommit", t.Commit, ErrTxnEnded},
	}
	for _, op := range ops {
		if got := op.run(); !errors.Is(got, op.want) {
			return fmt.Errorf("%s = %v, want %v", op.name, got, op.want)
		}
	}
	if errors.Is(ErrNoActiveTxn, ErrEmptyKey) || errors.Is(ErrEmptyKey, ErrTxnEnded) ||
		errors.Is(ErrNoActiveTxn, ErrTxnEnded) {
		return fmt.Errorf("sentinels not distinct")
	}
	return eqState(db.Committed(), before)
}
