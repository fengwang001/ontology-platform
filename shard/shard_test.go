package shard

import (
	"sync"
	"testing"

	"ontology/wal"
)

func TestNewAndTotal(t *testing.T) {
	s := New(4, 100)
	if s.Total() != 400 {
		t.Fatalf("Total = %d, want 400", s.Total())
	}
	if s.Len() != 4 {
		t.Fatalf("Len = %d, want 4", s.Len())
	}
	if got := s.Balance(2); got != 100 {
		t.Fatalf("Balance(2) = %d, want 100", got)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	s := New(2, 100)
	r := wal.Record{Seq: 1, Txn: 7, Shard: 0, Delta: -30}
	if !s.Apply(r) {
		t.Fatal("first Apply should report applied")
	}
	if s.Apply(r) {
		t.Fatal("duplicate Txn must be skipped")
	}
	if got := s.Balance(0); got != 70 {
		t.Fatalf("Balance(0) = %d, want 70", got)
	}
	if got := s.LastTxn(0); got != 7 {
		t.Fatalf("LastTxn(0) = %d, want 7", got)
	}
	// A lower Txn must also be skipped even if never seen directly.
	if s.Apply(wal.Record{Seq: 2, Txn: 3, Shard: 0, Delta: 999}) {
		t.Fatal("older Txn must be skipped")
	}
	if got := s.Balance(0); got != 70 {
		t.Fatalf("Balance(0) = %d after stale apply, want 70", got)
	}
}

func TestApplyOutOfRangeShard(t *testing.T) {
	s := New(1, 10)
	if s.Apply(wal.Record{Txn: 1, Shard: 5, Delta: 1}) {
		t.Fatal("out-of-range shard must not apply")
	}
	if s.Total() != 10 {
		t.Fatalf("Total = %d, want 10", s.Total())
	}
}

func TestConcurrentApplyKeepsTotal(t *testing.T) {
	// Each goroutine owns a private shard pair, so per-shard Txn ids
	// stay monotonic as the Apply contract requires.
	const pairs = 16
	const initial = 1000
	s := New(pairs*2, initial)
	var wg sync.WaitGroup
	for g := 0; g < pairs; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := uint64(g*1000 + i + 1)
				s.Apply(wal.Record{Txn: id, Shard: 2 * g, Delta: -1})
				s.Apply(wal.Record{Txn: id, Shard: 2*g + 1, Delta: 1})
			}
		}(g)
	}
	wg.Wait()
	if got := s.Total(); got != pairs*2*initial {
		t.Fatalf("Total = %d, want %d", got, pairs*2*initial)
	}
}
