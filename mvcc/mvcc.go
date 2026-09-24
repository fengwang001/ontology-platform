// Package mvcc implements read-committed MVCC visibility rules.
package mvcc

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/ver"
)

// Sentinel errors: each rejected operation is distinguishable.
var (
	ErrEmptyKey    = errors.New("mvcc: empty key")
	ErrEmptyValue  = errors.New("mvcc: empty value")
	ErrTxNotBegun  = errors.New("mvcc: transaction not begun")
	ErrTxCommitted = errors.New("mvcc: transaction already committed")
	ErrKeyNotFound = errors.New("mvcc: key not found")
)

const maxVersionsPerRead = 1 // only the latest committed version is kept

// Store is the in-memory MVCC table. Safe for concurrent use.
type Store struct {
	mu        sync.RWMutex
	c         int64                 // global commit counter
	nextTx    int                   // monotonic transaction id source
	pending   map[int]ver.Pending   // live transactions' write sets
	committed map[int]bool          // ids of committed transactions
	chains    map[string]*ver.Chain // key -> committed chain (latest only)
	checked   atomic.Int64          // versions inspected by the last read hit
}

func New() *Store {
	return &Store{pending: map[int]ver.Pending{}, committed: map[int]bool{}, chains: map[string]*ver.Chain{}}
}

// Begin allocates a fresh monotonically increasing transaction id.
func (s *Store) Begin() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTx++
	s.pending[s.nextTx] = ver.NewPending()
	return s.nextTx
}

// Write records (k, v) into tx's pending set; validation precedes mutation.
func (s *Store) Write(tx int, k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	if v == "" {
		return ErrEmptyValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.active(tx)
	if err != nil {
		return err
	}
	p.Set(k, v)
	return nil
}

// Commit increments C and atomically installs tx's pending writes as latest.
func (s *Store) Commit(tx int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.active(tx)
	if err != nil {
		return err
	}
	s.c++
	for k, v := range p {
		chain := s.chains[k]
		if chain == nil {
			chain = &ver.Chain{}
			s.chains[k] = chain
		}
		chain.Commit(v, s.c)
	}
	s.committed[tx], s.pending[tx] = true, nil
	return nil
}

// Read returns the latest committed value of k, re-read on every call.
func (s *Store) Read(k string) (string, error) {
	if k == "" {
		return "", ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestLocked(k)
}

// ReadTx: tx's own pending write wins, else latest committed; no snapshot.
func (s *Store) ReadTx(tx int, k string) (string, error) {
	if k == "" {
		return "", ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.active(tx)
	if err != nil {
		return "", err
	}
	if v, ok := p.Get(k); ok {
		s.checked.Store(0) // own pending hit inspects no committed version
		return v, nil
	}
	return s.latestLocked(k)
}

func (s *Store) latestLocked(k string) (string, error) {
	chain := s.chains[k]
	if chain == nil {
		s.checked.Store(0)
		return "", ErrKeyNotFound
	}
	s.checked.Store(1)
	v, _ := chain.Latest()
	return v, nil
}

func (s *Store) active(tx int) (ver.Pending, error) {
	p, ok := s.pending[tx]
	if !ok {
		return nil, ErrTxNotBegun
	}
	if s.committed[tx] {
		return nil, ErrTxCommitted
	}
	return p, nil
}

// CheckReadCost fails if one read after m commits inspects > 1 version.
func (s *Store) CheckReadCost(m int) error {
	for i := 0; i < m; i++ {
		if tx := s.Begin(); s.Write(tx, "cost-probe", "v"+strconv.Itoa(i)) != nil || s.Commit(tx) != nil {
			return errors.New("mvcc: probe commit failed")
		}
	}
	_, err := s.Read("cost-probe")
	if err == nil && s.checked.Load() > maxVersionsPerRead {
		err = errors.New("mvcc: read inspected too many versions")
	}
	return err
}
