package store

import (
	"testing"

	"ontology/txid"
)

func newStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	return New(txid.NewCounter(), cfg)
}

func commitKV(t *testing.T, s *Store, key, val string) {
	t.Helper()
	tx, err := s.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write(key, []byte(val)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotIsolationRepeatableRead(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1")

	snap, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release(snap)

	commitKV(t, s, "k", "v2") // 快照建立后的提交

	for i := 0; i < 3; i++ {
		got, lk := s.ReadAt(snap, "k")
		if lk != LookupFound || string(got) != "v1" {
			t.Fatalf("第 %d 次读应恒为 v1，得到 %q/%v", i, got, lk)
		}
	}

	snap2, _ := s.Begin()
	defer s.Release(snap2)
	got, lk := s.ReadAt(snap2, "k")
	if lk != LookupFound || string(got) != "v2" {
		t.Fatalf("新快照应看到 v2，得到 %q/%v", got, lk)
	}
}

func TestUncommittedInvisibleAndReadOwnWrites(t *testing.T) {
	s := newStore(t, Config{})
	tx, err := s.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write("k", []byte("mine")); err != nil {
		t.Fatal(err)
	}

	if got, lk := tx.Read("k"); lk != LookupFound || string(got) != "mine" {
		t.Fatalf("读己之写应可见，得到 %q/%v", got, lk)
	}

	snap, _ := s.Begin()
	defer s.Release(snap)
	if _, lk := s.ReadAt(snap, "k"); lk != LookupNever {
		t.Fatalf("未提交写入对他人不可见，得到 %v", lk)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, lk := s.ReadAt(snap, "k"); lk != LookupNever {
		t.Fatalf("旧快照在对方提交后仍不可见（可重复读），得到 %v", lk)
	}
	snap2, _ := s.Begin()
	defer s.Release(snap2)
	if got, lk := s.ReadAt(snap2, "k"); lk != LookupFound || string(got) != "mine" {
		t.Fatalf("提交后新快照应可见，得到 %q/%v", got, lk)
	}
}

func TestRollbackLeavesNoResidue(t *testing.T) {
	s := newStore(t, Config{})
	tx, _ := s.BeginTx()
	if err := tx.Write("k", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	snap, _ := s.Begin()
	defer s.Release(snap)
	if _, lk := s.ReadAt(snap, "k"); lk != LookupNever {
		t.Fatalf("回滚后应对所有人不可见，得到 %v", lk)
	}
	st := s.Stats()
	if st.TotalVersions != 0 || s.KeyVersions("k") != 0 {
		t.Fatalf("回滚后不得有残留: %+v", st)
	}
	if err := s.checkConsistency(); err != nil {
		t.Fatalf("回滚后一致性: %v", err)
	}
	s.Reclaim()
	if _, lk := s.ReadAt(snap, "k"); lk != LookupNever {
		t.Fatalf("回收器不得把回滚残留误判为可见，得到 %v", lk)
	}
}

func TestDeleteVersusNeverExisted(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1")

	tx, _ := s.BeginTx()
	if err := tx.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	snap, _ := s.Begin()
	defer s.Release(snap)
	if _, lk := s.ReadAt(snap, "k"); lk != LookupDeleted {
		t.Fatalf("已删除应为 LookupDeleted，得到 %v", lk)
	}
	if _, lk := s.ReadAt(snap, "absent"); lk != LookupNever {
		t.Fatalf("从未存在应为 LookupNever，得到 %v", lk)
	}
	if LookupDeleted == LookupNever {
		t.Fatalf("两种结果必须可判定地区分")
	}
}
