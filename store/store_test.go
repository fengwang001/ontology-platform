package store

import (
	"errors"
	"testing"
)

func vv(ver, c int64, marker bool, r int64) Version {
	return Version{Ver: ver, C: c, Marker: marker, R: r}
}

func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
		vs   []Version
	}{
		{"empty key", nil, []Version{vv(1, 0, false, 0)}},
		{"nonpositive ver", []byte("k"), []Version{vv(0, 0, false, 0)}},
		{"dup ver", []byte("k"), []Version{vv(1, 0, false, 0), vv(1, 1, false, 0)}},
		{"c negative", []byte("k"), []Version{vv(1, -1, false, 0)}},
		{"c too big", []byte("k"), []Version{vv(1, 1_000_000_000_001, false, 0)}},
		{"c decreasing", []byte("k"), []Version{vv(1, 5, false, 0), vv(2, 4, false, 0)}},
		{"marker locked", []byte("k"), []Version{vv(1, 0, true, 10)}},
		{"negative retention", []byte("k"), []Version{vv(1, 0, false, -1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			if err := s.Load(tc.key, tc.vs); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestLoadSortedAndSnapshot(t *testing.T) {
	s := New()
	if err := s.Load([]byte("b"), []Version{vv(2, 2, false, 0), vv(1, 1, false, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Load([]byte("a"), []Version{vv(5, 9, false, 0)}); err != nil {
		t.Fatal(err)
	}
	if got := s.MaxVer(); got != 5 {
		t.Fatalf("MaxVer = %d, want 5", got)
	}
	vs, ok := s.Snapshot([]byte("b"))
	if !ok || vs[0].Ver != 1 || vs[1].Ver != 2 {
		t.Fatalf("snapshot not sorted: %+v ok=%v", vs, ok)
	}
	keys := s.KeysFrom(nil)
	if len(keys) != 2 || string(keys[0]) != "a" || string(keys[1]) != "b" {
		t.Fatalf("keys order wrong: %v", keys)
	}
	if ks := s.KeysFrom([]byte("b")); len(ks) != 1 || string(ks[0]) != "b" {
		t.Fatalf("KeysFrom cursor wrong: %v", ks)
	}
}

func TestRemoveAndFailInjection(t *testing.T) {
	s := New()
	if err := s.Load([]byte("k"), []Version{vv(1, 0, false, 0), vv(2, 0, false, 0), vv(3, 0, false, 0)}); err != nil {
		t.Fatal(err)
	}
	s.FailNext(2) // 第 2 次 Remove 失败
	if err := s.Remove([]byte("k"), 1); err != nil {
		t.Fatalf("first remove should succeed: %v", err)
	}
	err := s.Remove([]byte("k"), 2)
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("want ErrRemoveFailed, got %v", err)
	}
	vs, _ := s.Snapshot([]byte("k"))
	if len(vs) != 2 || vs[0].Ver != 2 || vs[1].Ver != 3 {
		t.Fatalf("failed remove must not delete: %+v", vs)
	}
	// 注入只生效一次。
	if err := s.Remove([]byte("k"), 2); err != nil {
		t.Fatalf("injection should be consumed: %v", err)
	}
}

func TestTryApplyCommitAndRollback(t *testing.T) {
	s := New()
	if err := s.Load([]byte("k"), []Version{vv(1, 0, false, 0), vv(2, 100, false, 0)}); err != nil {
		t.Fatal(err)
	}
	mv, err := s.TryApply([]byte("k"), Plan{AddMarker: true, MarkerC: 100, Remove: []int64{1}})
	if err != nil || mv != 3 {
		t.Fatalf("commit failed: mv=%d err=%v", mv, err)
	}
	if got := s.MaxVer(); got != 3 {
		t.Fatalf("MaxVer after commit = %d", got)
	}

	// Remove 失败：标记与删除整体撤销，maxVer 也不前进。
	s.FailNext(1)
	_, err = s.TryApply([]byte("k"), Plan{AddMarker: true, MarkerC: 100, Remove: []int64{2}})
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("want rollback error, got %v", err)
	}
	vs, _ := s.Snapshot([]byte("k"))
	if len(vs) != 2 || vs[0].Ver != 2 || vs[1].Ver != 3 {
		t.Fatalf("rollback state wrong: %+v", vs)
	}
	if got := s.MaxVer(); got != 3 {
		t.Fatalf("MaxVer must stay 3 after rollback, got %d", got)
	}
	// 回滚后重放：成功并复用版本号 4。
	mv, err = s.TryApply([]byte("k"), Plan{AddMarker: true, MarkerC: 100})
	if err != nil || mv != 4 {
		t.Fatalf("replay should succeed: mv=%d err=%v", mv, err)
	}
}

func TestRemoveLastVersionDeletesKey(t *testing.T) {
	s := New()
	if err := s.Load([]byte("k"), []Version{vv(1, 0, true, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TryApply([]byte("k"), Plan{Remove: []int64{1}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Snapshot([]byte("k")); ok {
		t.Fatal("key should disappear when no versions remain")
	}
	if ks := s.KeysFrom(nil); len(ks) != 0 {
		t.Fatalf("no keys expected, got %v", ks)
	}
}
