package shard

import (
	"testing"

	"ontology/wal"
)

// TestApplyIdempotencyIsPerShard 回归：幂等判定曾是全局 maxTxn，
// 导致同一 Txn 的入账记录在另一片被跳过、总额不再守恒。
// 修复后幂等按每片 lastTxn 判定：同 Txn 可在不同片各应用一次，
// 且仅在同一片上重放时才被跳过。
func TestApplyIdempotencyIsPerShard(t *testing.T) {
	s := New(2, 100)
	debit := wal.Record{Seq: 1, Txn: 1, Shard: 0, Delta: -30}
	credit := wal.Record{Seq: 2, Txn: 1, Shard: 1, Delta: 30}
	if !s.Apply(debit) {
		t.Fatal("debit should apply")
	}
	if !s.Apply(credit) {
		t.Fatal("credit on another shard with same Txn should apply")
	}
	if s.Total() != 200 {
		t.Fatalf("Total = %d, want 200", s.Total())
	}
	// 同片重放同一 Txn 仍须幂等跳过。
	if s.Apply(debit) || s.Apply(credit) {
		t.Fatal("replayed records must be skipped per shard")
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 {
		t.Fatalf("balances = %d,%d, want 70,130", s.Balance(0), s.Balance(1))
	}
	// 各片独立推进：片 1 应用过 Txn 1 后，片 0 的旧 Txn 判定不受影响。
	if s.Apply(wal.Record{Seq: 3, Txn: 1, Shard: 0, Delta: -5}) {
		t.Fatal("already-applied Txn on shard 0 must be skipped")
	}
	// 下一笔成对转账在两片都应正常应用。
	if !s.Apply(wal.Record{Seq: 4, Txn: 2, Shard: 0, Delta: -5}) {
		t.Fatal("newer Txn on shard 0 should apply")
	}
	if !s.Apply(wal.Record{Seq: 5, Txn: 2, Shard: 1, Delta: 5}) {
		t.Fatal("newer Txn on shard 1 should apply")
	}
	if s.Total() != 200 {
		t.Fatalf("Total = %d, want 200", s.Total())
	}
}
