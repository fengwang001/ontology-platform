// Package api is the public entry point to the in-memory MVCC store.
// All validation happens before any state mutation, so a rejected call leaves
// no trace; the three failure modes use distinct sentinel errors.
package api

import (
	"errors"
	"fmt"

	"ontology/mvcc"
)

// Sentinel errors: distinct and decidable via errors.Is.
var (
	ErrEmptyKey         = errors.New("mvcc: key must not be empty")
	ErrInvalidSnapshot  = errors.New("mvcc: snapshot is negative or greater than current version")
	ErrSnapshotInactive = errors.New("mvcc: snapshot is not active")
)

// MVCC is the outward-facing store.
type MVCC struct{ st *mvcc.Store }

// New constructs an empty store.
func New() *MVCC { return &MVCC{st: mvcc.New()} }

// Write adds an immutable version and returns the new global version.
func (m *MVCC) Write(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey // rejected before touching state
	}
	return m.st.Write(key, value), nil
}

// Snapshot registers and returns the current read point.
func (m *MVCC) Snapshot() int64 { return m.st.Snapshot() }

// Read returns the value visible at snapshot s: greatest ver <= s.
func (m *MVCC) Read(key string, s int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	if s < 0 || s > m.st.T() {
		return "", false, ErrInvalidSnapshot
	}
	v, ok := m.st.Read(key, s)
	return v, ok, nil
}

// Release unregisters a snapshot; rejecting one that was never active.
func (m *MVCC) Release(s int64) error {
	if s < 0 || s > m.st.T() {
		return ErrInvalidSnapshot
	}
	if !m.st.Release(s) {
		return ErrSnapshotInactive
	}
	return nil
}

// Collect reclaims versions no active snapshot can see; returns the count.
func (m *MVCC) Collect() int { return m.st.Collect() }

// SelfCheck replays a built-in sequence and verifies the four invariants.
func (m *MVCC) SelfCheck() error {
	w := func(k, v string) int64 {
		t, err := m.Write(k, v)
		if err != nil {
			panic(err)
		}
		return t
	}
	// 1/2. Snapshot isolation + naive reference over the eight-step trace.
	w("k", "v1")
	A := m.Snapshot()
	w("k", "v2")
	B := m.Snapshot()
	w("k", "v3")
	if v, ok, _ := m.Read("k", A); !ok || v != "v1" {
		return fmt.Errorf("isolation: Read(k,A)=%q,%v want v1", v, ok)
	}
	if v, ok, _ := m.Read("k", B); !ok || v != "v2" {
		return fmt.Errorf("naive: Read(k,B)=%q,%v want v2", v, ok)
	}
	// 3. Collect safety: B must read identically after A is released.
	if err := m.Release(A); err != nil {
		return err
	}
	if n := m.Collect(); n != 1 {
		return fmt.Errorf("collect: removed %d want 1", n)
	}
	if v, ok, _ := m.Read("k", B); !ok || v != "v2" {
		return fmt.Errorf("collect safety: Read(k,B)=%q,%v want v2", v, ok)
	}
	// 4. Rejected operations are distinct errors and leave no trace.
	if err := selfCheckRejections(m); err != nil {
		return err
	}
	return nil
}

func selfCheckRejections(m *MVCC) error {
	tBefore := m.st.T()
	if _, err := m.Write("", "x"); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("empty key err=%v", err)
	}
	if _, _, err := m.Read("k", -1); !errors.Is(err, ErrInvalidSnapshot) {
		return fmt.Errorf("negative snapshot err=%v", err)
	}
	if _, _, err := m.Read("k", tBefore+1); !errors.Is(err, ErrInvalidSnapshot) {
		return fmt.Errorf("future snapshot err=%v", err)
	}
	if err := m.Release(tBefore + 1); !errors.Is(err, ErrInvalidSnapshot) {
		return fmt.Errorf("release future err=%v", err)
	}
	if err := m.Release(0); !errors.Is(err, ErrSnapshotInactive) { // 0 never active
		return fmt.Errorf("release inactive err=%v", err)
	}
	if m.st.T() != tBefore {
		return fmt.Errorf("state changed by rejected ops: t %d->%d", tBefore, m.st.T())
	}
	// Store still usable after rejections.
	if _, err := m.Write("k2", "live"); err != nil {
		return fmt.Errorf("store unusable after rejection: %w", err)
	}
	return nil
}
