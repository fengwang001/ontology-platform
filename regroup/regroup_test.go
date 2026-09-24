package regroup

import (
	"testing"

	"ontology/txn"
)

func ev(k txn.Kind, tx int64, d string) txn.Event { return txn.Event{Kind: k, Tx: tx, Data: d} }

// 证明按事务分别缓冲：COMMIT/ROLLBACK 检查的行数只取决于该事务自身，
// 不随其他进行中事务的缓冲量 m 增长。
func TestCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New(m + 10)
		r.Feed(ev(txn.Begin, 1, ""))
		for i := 0; i < m; i++ {
			r.Feed(ev(txn.Row, 1, "x"))
		}
		r.Feed(ev(txn.Begin, 2, ""))
		r.Feed(ev(txn.Row, 2, "y"))
		if _, err := r.Feed(ev(txn.Commit, 2, "")); err != nil {
			t.Fatal(err)
		}
		if r.lastChecked > 1 { // 1 行 + 与 m 无关的常数 0
			t.Fatalf("m=%d: commit of 1-row tx checked %d rows", m, r.lastChecked)
		}
		if _, err := r.Feed(ev(txn.Commit, 1, "")); err != nil {
			t.Fatal(err)
		}
		if r.lastChecked > m { // m 行 + 常数 0
			t.Fatalf("m=%d: commit of m-row tx checked %d rows", m, r.lastChecked)
		}
	}
}

// ROLLBACK 路径同样只检查被丢弃事务自身的行。
func TestCheckedBoundedRollback(t *testing.T) {
	r := New(1010)
	r.Feed(ev(txn.Begin, 1, ""))
	for i := 0; i < 1000; i++ {
		r.Feed(ev(txn.Row, 1, "x"))
	}
	r.Feed(ev(txn.Begin, 2, ""))
	r.Feed(ev(txn.Row, 2, "y"))
	if _, err := r.Feed(ev(txn.Rollback, 2, "")); err != nil {
		t.Fatal(err)
	}
	if r.lastChecked > 1 {
		t.Fatalf("rollback of 1-row tx checked %d rows", r.lastChecked)
	}
}
