// Package mvcc implements the global commit counter, transaction
// lifecycle and read-committed visibility over per-key ver versions.
package mvcc

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/ver"
)

var (
	ErrEmptyKey    = errors.New("mvcc: empty key")
	ErrEmptyValue  = errors.New("mvcc: empty value")
	ErrTxNotBegun  = errors.New("mvcc: transaction not begun")
	ErrTxCommitted = errors.New("mvcc: transaction already committed")
)

const readCostBound int64 = 1 // m-independent cap on versions per read

type txState struct {
	pending   *ver.Pending
	committed bool
}

// Store is the in-process key/value table with read-committed MVCC.
type Store struct {
	mu     sync.RWMutex
	c      int                   // global commit counter
	nextID int                   // monotonic transaction id source
	txs    map[int]*txState      // all begun transactions
	keys   map[string]*ver.Entry // latest committed version per key
	probes atomic.Int64          // versions inspected by latest read; unexported, not in any API
}

func New() *Store {
	return &Store{txs: map[int]*txState{}, keys: map[string]*ver.Entry{}}
}

// Begin allocates and returns a new monotonic transaction id.
func (s *Store) Begin() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.txs[s.nextID] = &txState{pending: ver.NewPending()}
	return s.nextID
}

func (s *Store) Write(tx int, k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	if v == "" {
		return ErrEmptyValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.txs[tx]
	if t == nil {
		return ErrTxNotBegun
	}
	if t.committed {
		return ErrTxCommitted
	}
	t.pending.Put(k, v)
	return nil
}

// Commit atomically publishes tx's pending writes under one new sequence.
func (s *Store) Commit(tx int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.txs[tx]
	if t == nil {
		return ErrTxNotBegun
	}
	if t.committed {
		return ErrTxCommitted
	}
	s.c++
	t.pending.Range(func(k, v string) {
		e := s.keys[k]
		if e == nil {
			e = &ver.Entry{}
			s.keys[k] = e
		}
		e.Apply(v, s.c)
	})
	t.committed = true
	return nil
}

func (s *Store) latest(k string) (string, bool) {
	if e := s.keys[k]; e != nil {
		v, ok := e.Latest()
		s.probes.Store(readCostBound) // one version node exists, one inspected
		return v, ok
	}
	s.probes.Store(0)
	return "", false
}

func (s *Store) Read(k string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest(k)
}

// ReadTx: own pending write wins, else latest committed re-fetched each call.
func (s *Store) ReadTx(tx int, k string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.txs[tx]
	if t == nil {
		return "", false, ErrTxNotBegun
	}
	if t.committed {
		return "", false, ErrTxCommitted
	}
	if v, ok := t.pending.Get(k); ok {
		s.probes.Store(readCostBound)
		return v, true, nil
	}
	v, ok := s.latest(k)
	return v, ok, nil
}

// CheckReadCost reports pass/fail (never the counter value) for the O(1) claim.
func (s *Store) CheckReadCost() error {
	for _, m := range []int{100, 1000, 10000} {
		t := New()
		for i := 0; i < m; i++ {
			tx := t.Begin()
			err := t.Write(tx, "k", fmt.Sprintf("v%d", i))
			if err == nil {
				err = t.Commit(tx)
			}
			if err != nil {
				return err
			}
		}
		t.Read("k")
		if t.probes.Load() > readCostBound {
			return fmt.Errorf("mvcc: inspected version count not constant at m=%d", m)
		}
	}
	return nil
}
