// Package ssi implements serializable snapshot isolation on top of the
// dependency-free kv package: begin-time snapshots, read/write buffers,
// rw-conflict detection at commit and full rollback on write skew.
package ssi

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/kv"
)

// Sentinel errors: the three operation-failure classes are pairwise distinct;
// ErrConflict is the distinct outcome of a rolled-back (write-skew) txn.
var (
	ErrNoTxn       = errors.New("ssi: no active transaction")
	ErrEmptyKey    = errors.New("ssi: empty key")
	ErrTxnFinished = errors.New("ssi: transaction already finished")
	ErrConflict    = errors.New("ssi: serializability conflict, rolled back")
)

type Engine struct {
	st       *kv.Store
	commitMu sync.Mutex // serializes "detect against history then apply"
	// lastChecked counts committed txn records examined by the latest Commit.
	// Unexported and unreachable via any method: kv key indexes answer the
	// existence questions directly, so no record opens and it never grows.
	lastChecked int
}
type Txn struct {
	e                 *Engine
	snap              map[string]string
	snapVer           int
	reads, writes     map[string]string
	in, out, finished bool
}

// NewEngine returns an engine at version 0 holding seed values; seed is
// initial state rather than a committed txn and retains no record. nil=empty.
func NewEngine(seed map[string]string) *Engine { return &Engine{st: kv.New(seed)} }

// Committed returns a copy of the committed key/value state.
func (e *Engine) Committed() map[string]string { return e.st.Committed() }

// Begin captures a snapshot and the current global version.
func (e *Engine) Begin() *Txn {
	snap, ver := e.st.Snapshot()
	return &Txn{e: e, snap: snap, snapVer: ver, reads: map[string]string{}, writes: map[string]string{}}
}

// Read returns the own buffered write if present (read-your-writes, not
// recorded), otherwise the snapshot value, recording (key, value) in reads.
func (t *Txn) Read(key string) (string, error) {
	if t == nil {
		return "", ErrNoTxn
	}
	if t.finished {
		return "", ErrTxnFinished
	}
	if key == "" {
		return "", ErrEmptyKey
	}
	if v, ok := t.writes[key]; ok {
		return v, nil
	}
	v := t.snap[key]
	t.reads[key] = v
	return v, nil
}

// Write buffers (key, val); invisible to others until commit.
func (t *Txn) Write(key, val string) error {
	if t == nil {
		return ErrNoTxn
	}
	if t.finished {
		return ErrTxnFinished
	}
	if key == "" {
		return ErrEmptyKey
	}
	t.writes[key] = val
	return nil
}

// Commit detects rw edges against committed history. Both an in-edge and an
// out-edge together mean a dangerous structure (write skew): the txn is
// rolled back, its sets discarded and nothing applied. Otherwise its write
// set is applied and read/write sets are retained for future checks.
func (t *Txn) Commit() error {
	if t == nil {
		return ErrNoTxn
	}
	if t.finished {
		return ErrTxnFinished
	}
	t.finished = true
	e := t.e
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.lastChecked = 0 // indexes answer existence directly; no record opened
	t.in, t.out = e.st.Conflicts(t.snapVer, t.reads, t.writes)
	if t.in && t.out {
		t.reads, t.writes = map[string]string{}, map[string]string{} // rollback
		return ErrConflict
	}
	e.st.Apply(&kv.Record{Read: clone(t.reads), Write: clone(t.writes)})
	return nil
}

// String renders snapshot version, sets and conflict flags for diagnostics.
// It never exposes Engine.lastChecked.
func (t *Txn) String() string {
	if t == nil {
		return "<no txn>"
	}
	return fmt.Sprintf("sv%d R{%s} W{%s} i%do%d", t.snapVer,
		setStr(t.reads), setStr(t.writes), b2i(t.in), b2i(t.out))
}

func clone(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func setStr(m map[string]string) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	ps := make([]string, 0, len(ks))
	for _, k := range ks {
		ps = append(ps, k+":"+m[k])
	}
	return strings.Join(ps, " ")
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
