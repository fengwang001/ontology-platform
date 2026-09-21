// Package shard holds per-shard balances with idempotent application.
package shard

import (
	"sync"

	"ontology/wal"
)

// Set is a fixed number of shards, each with its own balance and the
// highest transaction id it has applied.
type Set struct {
	mu       sync.Mutex
	balances []int64
	lastTxn  []uint64
}

// New creates n shards, each starting with the initial balance.
func New(n int, initial int64) *Set {
	s := &Set{
		balances: make([]int64, n),
		lastTxn:  make([]uint64, n),
	}
	for i := range s.balances {
		s.balances[i] = initial
	}
	return s
}

// Apply applies r to its shard. A record whose Txn was already applied
// (Txn <= LastTxn of that shard) is skipped and reports false, which
// makes replay idempotent.
func (s *Set) Apply(r wal.Record) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Shard < 0 || r.Shard >= len(s.balances) {
		return false
	}
	if r.Txn <= s.lastTxn[r.Shard] {
		return false
	}
	s.balances[r.Shard] += r.Delta
	s.lastTxn[r.Shard] = r.Txn
	return true
}

// Balance returns the current balance of shard i.
func (s *Set) Balance(i int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.balances[i]
}

// Total returns the sum of all shard balances.
func (s *Set) Total() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sum int64
	for _, b := range s.balances {
		sum += b
	}
	return sum
}

// LastTxn returns the highest transaction id applied to shard i.
func (s *Set) LastTxn(i int) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTxn[i]
}

// Len returns the number of shards.
func (s *Set) Len() int {
	return len(s.balances)
}
