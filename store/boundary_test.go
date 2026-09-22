package store

import "testing"

// 第 4 条：可见性边界左闭右开。
// 快照点 = 建立时 Current()+1。下一个开始的事务号恰好等于该快照点，
// 它的提交必须不可见（等于快照点，取不到）。
func TestCommitExactlyAtSnapshotPointInvisible(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1") // 事务号 1，Current=1
	snap, err := s.Begin()    // 快照点 = 2
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release(snap)
	tx, _ := s.BeginTx() // 事务号 2 == 快照点
	if tx.ID() != snap.Point() {
		t.Fatalf("构造失败：事务号 %v 应恰好等于快照点 %v", tx.ID(), snap.Point())
	}
	if err := tx.Write("k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, lk := s.ReadAt(snap, "k"); lk != LookupFound || string(got) != "v1" {
		t.Fatalf("恰好等于快照点的提交不可见，应仍为 v1，得到 %q/%v", got, lk)
	}
}

// 第 4 条：活跃集合子句。事务在快照建立前开始（事务号 < 快照点），
// 但在快照建立后才提交，因其在活跃集合中而不可见。
func TestActiveSetClause(t *testing.T) {
	s := newStore(t, Config{})
	tx, err := s.BeginTx() // 事务号 1，先开始
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write("k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Begin() // 快照点 = 2，活跃集合 = {1}
	defer s.Release(snap)
	if err := tx.Commit(); err != nil { // 快照建立后才提交
		t.Fatal(err)
	}
	if _, lk := s.ReadAt(snap, "k"); lk != LookupNever {
		t.Fatalf("提交事务在活跃集合中，即使事务号小于快照点也不可见，得到 %v", lk)
	}
	snap2, _ := s.Begin() // 新快照活跃集合为空
	defer s.Release(snap2)
	if got, lk := s.ReadAt(snap2, "k"); lk != LookupFound || string(got) != "v1" {
		t.Fatalf("新快照应可见，得到 %q/%v", got, lk)
	}
}

// 左闭：提交事务号严格小于快照点且不在活跃集合时可见。
func TestCommittedBeforeSnapshotVisible(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1") // 事务号 1
	commitKV(t, s, "k", "v2") // 事务号 2
	snap, _ := s.Begin()      // 快照点 = 3
	defer s.Release(snap)
	if got, lk := s.ReadAt(snap, "k"); lk != LookupFound || string(got) != "v2" {
		t.Fatalf("快照建立前已提交的最新版本应可见，得到 %q/%v", got, lk)
	}
}
