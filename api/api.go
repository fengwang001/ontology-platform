// Package api is the public facade over the SSI engine; it depends only on
// ssi and exposes New, Begin/Read/Write/Commit, Committed and SelfCheck.
package api

import (
	"fmt"

	"ontology/ssi"
)

// DB is the serializable snapshot-isolation key/value store.
type DB struct{ eng *ssi.Engine }

// Txn is a Begin handle. A nil *Txn (an operation without a successful Begin)
// fails every operation with ssi.ErrNoTxn.
type Txn struct{ t *ssi.Txn }

// New returns an empty database.
func New() *DB { return &DB{eng: ssi.NewEngine(nil)} }

// Begin starts a transaction on a fresh snapshot.
func (d *DB) Begin() *Txn { return &Txn{t: d.eng.Begin()} }

// Read implements read-your-writes over the begin-time snapshot.
func (t *Txn) Read(key string) (string, error) {
	if t == nil || t.t == nil {
		return "", ssi.ErrNoTxn
	}
	return t.t.Read(key)
}

// Write buffers a write until commit.
func (t *Txn) Write(key, val string) error {
	if t == nil || t.t == nil {
		return ssi.ErrNoTxn
	}
	return t.t.Write(key, val)
}

// Commit applies the write set or rolls the transaction back on write skew.
func (t *Txn) Commit() error {
	if t == nil || t.t == nil {
		return ssi.ErrNoTxn
	}
	return t.t.Commit()
}

// String renders snapshot version, sets and in/out flags for diagnostics.
func (t *Txn) String() string {
	if t == nil || t.t == nil {
		return "<no txn>"
	}
	return t.t.String()
}

// Committed returns a copy of the committed key/value state.
func (d *DB) Committed() map[string]string { return d.eng.Committed() }

// SelfCheck runs the built-in 14-step history and the four-invariant checks on
// isolated engines; it never mutates d.
func (d *DB) SelfCheck() error {
	if err := scenario(ssi.NewEngine(map[string]string{"x": "10", "y": "10", "z": "5"})); err != nil {
		return err
	}
	return checkNoTrace(ssi.NewEngine(nil))
}

// scenario drives the canonical history (all begin against version 0) and
// asserts x=-10,y=10,z=99, T2 rolled back, T3 reading its own z=99.
func scenario(e *ssi.Engine) error {
	t1, t2, t3 := e.Begin(), e.Begin(), e.Begin()
	if _, err := t1.Read("x"); err != nil {
		return err
	}
	if _, err := t2.Read("x"); err != nil {
		return err
	}
	if _, err := t1.Read("y"); err != nil {
		return err
	}
	if _, err := t2.Read("y"); err != nil {
		return err
	}
	if err := t3.Write("z", "99"); err != nil {
		return err
	}
	if v, err := t3.Read("z"); err != nil || v != "99" {
		return fmt.Errorf("selfcheck: read-your-writes z=%q: %w", v, err)
	}
	if err := t1.Write("x", "-10"); err != nil {
		return err
	}
	if err := t1.Commit(); err != nil {
		return fmt.Errorf("selfcheck: T1 should commit: %w", err)
	}
	if err := t3.Commit(); err != nil {
		return fmt.Errorf("selfcheck: T3 should commit: %w", err)
	}
	if err := t2.Write("y", "-10"); err != nil {
		return err
	}
	if err := t2.Commit(); err != ssi.ErrConflict {
		return fmt.Errorf("selfcheck: T2 must roll back, got %v", err)
	}
	got := e.Committed()
	want := map[string]string{"x": "-10", "y": "10", "z": "99"}
	if len(got) != len(want) {
		return fmt.Errorf("selfcheck: state %v", got)
	}
	for k, w := range want {
		if got[k] != w {
			return fmt.Errorf("selfcheck: %s=%q want %q", k, got[k], w)
		}
	}
	return nil
}

// checkNoTrace asserts the three distinct sentinels and that rejected ops
// leave no state change while the engine stays usable.
func checkNoTrace(e *ssi.Engine) error {
	var nilTxn *ssi.Txn
	if _, err := nilTxn.Read("k"); err != ssi.ErrNoTxn {
		return fmt.Errorf("selfcheck: nil read err=%v", err)
	}
	t := e.Begin()
	if _, err := t.Read(""); err != ssi.ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty-read err=%v", err)
	}
	if err := t.Write("", "v"); err != ssi.ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty-write err=%v", err)
	}
	if err := t.Write("k", "v"); err != nil {
		return err
	}
	if err := t.Commit(); err != nil {
		return err
	}
	if _, err := t.Read("k"); err != ssi.ErrTxnFinished {
		return fmt.Errorf("selfcheck: post-commit read err=%v", err)
	}
	if err := t.Commit(); err != ssi.ErrTxnFinished {
		return fmt.Errorf("selfcheck: double commit err=%v", err)
	}
	if len(e.Committed()) != 1 || e.Committed()["k"] != "v" {
		return fmt.Errorf("selfcheck: rejected ops left traces: %v", e.Committed())
	}
	return nil
}
