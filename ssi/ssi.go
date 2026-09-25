// Package ssi implements serializable snapshot isolation transactions:
// snapshot reads, buffered writes, and commit-time rw-conflict detection.
package ssi

import (
	"errors"
	"sync"

	"ontology/kv"
)

var (
	ErrNoActiveTxn = errors.New("ssi: no active transaction")
	ErrEmptyKey    = errors.New("ssi: empty key")
	ErrTxnEnded    = errors.New("ssi: transaction already ended")
	ErrConflict    = errors.New("ssi: write skew detected, rolled back")
)

// Manager owns the committed store and serializes commits.
type Manager struct {
	mu       sync.Mutex // serializes Commit
	store    *kv.Store
	examined int // committed txns examined by the last Commit (index probes: 0)
}

// New returns a Manager over an empty store.
func New() *Manager { return NewManager(kv.New(nil)) }

// NewManager returns a Manager over s.
func NewManager(s *kv.Store) *Manager { return &Manager{store: s} }

// Committed returns a copy of the committed state.
func (m *Manager) Committed() map[string]string { return m.store.Committed() }

// Begin starts a transaction on a snapshot of the committed state.
func (m *Manager) Begin() *Txn {
	snap, sv := m.store.Snapshot()
	return &Txn{mgr: m, snap: snap, sv: sv, rs: map[string]string{}, ws: map[string]string{}}
}

// Txn is one in-flight transaction.
type Txn struct {
	mgr     *Manager
	snap    map[string]string
	sv      int
	rs, ws  map[string]string
	in, out bool
	done    bool
}

// Flags reports the rw-conflict flags set at commit time.
func (t *Txn) Flags() (in, out bool) {
	if t == nil {
		return false, false
	}
	return t.in, t.out
}

// Read returns own buffered write if present, else the snapshot value.
func (t *Txn) Read(key string) (string, error) {
	if t == nil {
		return "", ErrNoActiveTxn
	}
	if t.done {
		return "", ErrTxnEnded
	}
	if key == "" {
		return "", ErrEmptyKey
	}
	if v, ok := t.ws[key]; ok { // read-own-write: not recorded in read set
		return v, nil
	}
	v := t.snap[key]
	t.rs[key] = v
	return v, nil
}

// Write buffers val under key; invisible to others until commit.
func (t *Txn) Write(key, val string) error {
	if t == nil {
		return ErrNoActiveTxn
	}
	if t.done {
		return ErrTxnEnded
	}
	if key == "" {
		return ErrEmptyKey
	}
	t.ws[key] = val
	return nil
}

// Commit runs rw-conflict detection against committed transactions and
// either applies the write set or rolls back leaving no trace.
func (t *Txn) Commit() error {
	if t == nil {
		return ErrNoActiveTxn
	}
	if t.done {
		return ErrTxnEnded
	}
	m := t.mgr
	m.mu.Lock()
	defer m.mu.Unlock()
	m.examined = 0        // index probes decide without visiting committed records
	for k := range t.ws { // in: some committed U read a key we overwrite
		if m.store.ReadTouched(k) {
			t.in = true
			break
		}
	}
	for k := range t.rs { // out: a key we read was committed after our snapshot
		if m.store.WrittenAfter(k, t.sv) {
			t.out = true
			break
		}
	}
	t.done = true
	if t.in && t.out { // dangerous structure: roll back, discard everything
		t.rs, t.ws = nil, nil
		return ErrConflict
	}
	m.store.Apply(t.rs, t.ws)
	return nil
}
