package shard

import (
	"testing"

	"ontology/wal"
)

// 回归：幂等判定曾误用全局最大 Txn，导致同一 Txn 落在另一片上的
// 记录被当成“已应用”而跳过（转账只扣不入，总额不再守恒）。
// 根因修复：按片比较 lastTxn[shard]。
func TestApplySameTxnAcrossShardsRegression(t *testing.T) {
	s := New(3, 100)
	// 同一 Txn 的两半分别落在片 0 与片 2，两半都必须应用。
	if !s.Apply(wal.Record{Seq: 1, Txn: 1, Shard: 0, Delta: -25}) {
		t.Fatal("debit half must apply")
	}
	if !s.Apply(wal.Record{Seq: 2, Txn: 1, Shard: 2, Delta: 25}) {
		t.Fatal("credit half on another shard must apply")
	}
	if s.Total() != 300 {
		t.Fatalf("Total = %d, want 300", s.Total())
	}
	// 后续更大的 Txn 在片 1 上应用后，已应用过的 Txn 在片 0/2 上仍须
	// 被跳过，但片 1 自己的新 Txn 不受其他片进度影响。
	if !s.Apply(wal.Record{Seq: 3, Txn: 5, Shard: 1, Delta: -10}) {
		t.Fatal("newer txn on shard 1 must apply")
	}
	if s.Apply(wal.Record{Seq: 4, Txn: 1, Shard: 0, Delta: 999}) {
		t.Fatal("duplicate txn on shard 0 must be skipped")
	}
	if s.Apply(wal.Record{Seq: 5, Txn: 1, Shard: 2, Delta: 999}) {
		t.Fatal("duplicate txn on shard 2 must be skipped")
	}
	if s.Balance(0) != 75 || s.Balance(1) != 90 || s.Balance(2) != 125 {
		t.Fatalf("balances = %d,%d,%d", s.Balance(0), s.Balance(1), s.Balance(2))
	}
	if s.Total() != 290 {
		t.Fatalf("Total = %d, want 290", s.Total())
	}
}
