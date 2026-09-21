package shard

import (
	"sync"
	"testing"

	"ontology/wal"
)

func TestApplyAndBalance(t *testing.T) {
	s := New(3, 100)
	if s.Total() != 300 {
		t.Fatalf("Total = %d, want 300", s.Total())
	}
	if !s.Apply(wal.Record{Seq: 1, Txn: 1, Shard: 0, Delta: -30}) {
		t.Fatal("first Apply should report applied")
	}
	if !s.Apply(wal.Record{Seq: 2, Txn: 1, Shard: 1, Delta: 30}) {
		t.Fatal("first Apply should report applied")
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 || s.Balance(2) != 100 {
		t.Fatalf("balances = %d,%d,%d", s.Balance(0), s.Balance(1), s.Balance(2))
	}
	if s.Total() != 300 {
		t.Fatalf("Total after transfer = %d, want 300", s.Total())
	}
	if s.LastTxn(0) != 1 || s.LastTxn(1) != 1 || s.LastTxn(2) != 0 {
		t.Fatalf("LastTxn = %d,%d,%d", s.LastTxn(0), s.LastTxn(1), s.LastTxn(2))
	}
}

func TestApplyIdempotentPerTxn(t *testing.T) {
	s := New(2, 50)
	r := wal.Record{Seq: 1, Txn: 7, Shard: 0, Delta: -10}
	if !s.Apply(r) {
		t.Fatal("first Apply should apply")
	}
	if s.Apply(r) {
		t.Fatal("duplicate Txn must be skipped")
	}
	// 更旧的 Txn 也必须跳过。
	if s.Apply(wal.Record{Seq: 2, Txn: 3, Shard: 0, Delta: 999}) {
		t.Fatal("older Txn must be skipped")
	}
	if s.Balance(0) != 40 {
		t.Fatalf("Balance = %d, want 40", s.Balance(0))
	}
	if s.LastTxn(0) != 7 {
		t.Fatalf("LastTxn = %d, want 7", s.LastTxn(0))
	}
	// 幂等判定是分片独立的：同一 Txn 在另一片仍可应用。
	if !s.Apply(wal.Record{Seq: 2, Txn: 7, Shard: 1, Delta: 10}) {
		t.Fatal("same Txn on another shard should apply")
	}
	if s.Total() != 100 {
		t.Fatalf("Total = %d, want 100", s.Total())
	}
}

func TestApplyOutOfRangeShard(t *testing.T) {
	s := New(2, 10)
	if s.Apply(wal.Record{Seq: 1, Txn: 1, Shard: 5, Delta: 1}) {
		t.Fatal("out-of-range shard must not apply")
	}
	if s.Apply(wal.Record{Seq: 1, Txn: 1, Shard: -1, Delta: 1}) {
		t.Fatal("negative shard must not apply")
	}
	if s.Total() != 20 {
		t.Fatalf("Total = %d, want 20", s.Total())
	}
}

func TestConcurrentApplyKeepsTotal(t *testing.T) {
	const n = 8
	s := New(n, 1000)
	var wg sync.WaitGroup
	var txn uint64
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				from := (g + i) % n
				to := (g + i + 1) % n
				// Txn 分配与成对应用保持同序，模拟协调器的串行语义。
				mu.Lock()
				txn++
				tx := txn
				s.Apply(wal.Record{Txn: tx, Shard: from, Delta: -1})
				s.Apply(wal.Record{Txn: tx, Shard: to, Delta: 1})
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	if s.Total() != n*1000 {
		t.Fatalf("Total = %d, want %d", s.Total(), n*1000)
	}
}
