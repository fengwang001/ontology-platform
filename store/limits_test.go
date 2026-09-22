package store

import (
	"errors"
	"testing"
)

func snapshotOf(t *testing.T, s *Store, keys []string) (Stats, []int) {
	t.Helper()
	lens := make([]int, len(keys))
	for i, k := range keys {
		lens[i] = s.KeyVersions(k)
	}
	return s.Stats(), lens
}

func assertStateUnchanged(t *testing.T, s *Store, keys []string, before Stats, beforeLens []int) {
	t.Helper()
	after, afterLens := snapshotOf(t, s, keys)
	if after != before {
		t.Fatalf("拒绝不得改变状态: %+v -> %+v", before, after)
	}
	for i := range keys {
		if afterLens[i] != beforeLens[i] {
			t.Fatalf("键 %s 版本链长度变化: %d -> %d", keys[i], beforeLens[i], afterLens[i])
		}
	}
}

func TestChainLenLimit(t *testing.T) {
	s := newStore(t, Config{MaxChainLen: 2})
	commitKV(t, s, "k", "v1")
	commitKV(t, s, "k", "v2")
	tx, _ := s.BeginTx()
	if err := tx.Write("k", []byte("v3")); err != nil {
		t.Fatal(err)
	}
	before, lens := snapshotOf(t, s, []string{"k"})
	err := tx.Commit()
	if !errors.Is(err, ErrChainLenExceeded) {
		t.Fatalf("应返回 ErrChainLenExceeded，得到 %v", err)
	}
	if errors.Is(err, ErrVersionLimit) || errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("三类超限错误必须可判定地区分")
	}
	assertStateUnchanged(t, s, []string{"k"}, before, lens)
	_ = tx.Rollback()
}

func TestTotalVersionLimit(t *testing.T) {
	s := newStore(t, Config{MaxVersions: 2})
	commitKV(t, s, "a", "1")
	commitKV(t, s, "b", "2")
	tx, _ := s.BeginTx()
	if err := tx.Write("c", []byte("3")); err != nil {
		t.Fatal(err)
	}
	before, lens := snapshotOf(t, s, []string{"a", "b", "c"})
	err := tx.Commit()
	if !errors.Is(err, ErrVersionLimit) {
		t.Fatalf("应返回 ErrVersionLimit，得到 %v", err)
	}
	if errors.Is(err, ErrChainLenExceeded) || errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("三类超限错误必须可判定地区分")
	}
	assertStateUnchanged(t, s, []string{"a", "b", "c"}, before, lens)
	_ = tx.Rollback()
}

func TestSnapshotLimit(t *testing.T) {
	s := newStore(t, Config{MaxSnapshots: 1})
	snap, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release(snap)
	before, lens := snapshotOf(t, s, nil)
	_, err = s.Begin()
	if !errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("应返回 ErrSnapshotLimit，得到 %v", err)
	}
	if errors.Is(err, ErrChainLenExceeded) || errors.Is(err, ErrVersionLimit) {
		t.Fatalf("三类超限错误必须可判定地区分")
	}
	assertStateUnchanged(t, s, nil, before, lens)
}

// 三类错误两两不同。
func TestLimitErrorsDistinct(t *testing.T) {
	for _, pair := range [][2]error{
		{ErrChainLenExceeded, ErrVersionLimit},
		{ErrChainLenExceeded, ErrSnapshotLimit},
		{ErrVersionLimit, ErrSnapshotLimit},
	} {
		if errors.Is(pair[0], pair[1]) {
			t.Fatalf("%v 与 %v 必须可判定地区分", pair[0], pair[1])
		}
	}
}
