package shard

import (
	"testing"

	"ontology/wal"
)

// TestRegressionSameTxnAppliesOnBothShards 精确复现已修复根因：
// 幂等水位必须按分片独立。旧实现用全局 maxTxn 判定，同一 Txn 的入账
// 记录（另一分片）被扣款片的水位跳过，导致转账后总额凭空蒸发。
func TestRegressionSameTxnAppliesOnBothShards(t *testing.T) {
	s := New(2, 100)
	if !s.Apply(wal.Record{Seq: 1, Txn: 1, Shard: 0, Delta: -40}) {
		t.Fatal("debit leg must apply")
	}
	if !s.Apply(wal.Record{Seq: 2, Txn: 1, Shard: 1, Delta: 40}) {
		t.Fatal("credit leg on another shard must not be blocked by debit shard's Txn watermark")
	}
	if s.Balance(0) != 60 || s.Balance(1) != 140 {
		t.Fatalf("balances = %d,%d, want 60,140", s.Balance(0), s.Balance(1))
	}
	if s.Total() != 200 {
		t.Fatalf("Total = %d, want 200", s.Total())
	}
	// 同片重放仍必须幂等。
	if s.Apply(wal.Record{Seq: 3, Txn: 1, Shard: 1, Delta: 40}) {
		t.Fatal("same Txn on same shard must be skipped")
	}
	if s.Total() != 200 {
		t.Fatalf("Total after replay = %d, want 200", s.Total())
	}
}
