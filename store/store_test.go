package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name string
		key  string
		ver  Version
	}{
		{"empty key", "", Version{Ver: 1}},
		{"negative c", "a", Version{Ver: 1, C: -1}},
		{"c too large", "a", Version{Ver: 1, C: MaxClock + 1}},
		{"negative r", "a", Version{Ver: 1, R: -1}},
		{"marker with lock", "a", Version{Ver: 1, Marker: true, R: 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			if err := s.Load(tc.key, tc.ver); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLoadOrdering(t *testing.T) {
	s := New()
	if err := s.Load("a", Version{Ver: 5, C: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("a", Version{Ver: 5, C: 100}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate ver: got %v", err)
	}
	if err := s.Load("a", Version{Ver: 4, C: 100}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("decreasing ver: got %v", err)
	}
	if err := s.Load("a", Version{Ver: 6, C: 99}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("decreasing c: got %v", err)
	}
	if err := s.Load("a", Version{Ver: 6, C: 100}); err != nil {
		t.Fatalf("equal c allowed: %v", err)
	}
}

func TestRemoveHookAndNotFound(t *testing.T) {
	s := New()
	mustLoad(t, s, "a", Version{Ver: 1})
	s.SetRemoveHook(func(key string, ver uint64) bool { return key == "a" && ver == 1 })
	if err := s.Remove("a", 1); !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("injected: got %v", err)
	}
	if got := s.Versions("a"); len(got) != 1 {
		t.Fatalf("failed remove must not delete: %v", got)
	}
	s.SetRemoveHook(nil)
	if err := s.Remove("a", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: got %v", err)
	}
	if err := s.Remove("a", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.Keys(); len(got) != 0 {
		t.Fatalf("empty key must disappear: %v", got)
	}
}

func TestApplyRollback(t *testing.T) {
	s := New()
	mustLoad(t, s, "a", Version{Ver: 1, C: 10})
	mustLoad(t, s, "a", Version{Ver: 2, C: 20})
	mustLoad(t, s, "b", Version{Ver: 3, C: 30})
	before := s.Snapshot()
	// 追加标记（应得全局序号 4），删除 v1、v2，第二次删除注入失败。
	calls := 0
	s.SetRemoveHook(func(key string, ver uint64) bool {
		calls++
		return calls == 2
	})
	_, err := s.Apply("a", []int64{99}, []uint64{1, 2}, false)
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("got %v", err)
	}
	if got := s.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rollback mismatch:\n got %v\nwant %v", got, before)
	}
	// 注入撤销后重试：标记取新的全局序号 5（4 已被回滚消耗）。
	s.SetRemoveHook(nil)
	added, err := s.Apply("a", []int64{99}, []uint64{1, 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Ver != 5 || added[0].C != 99 || !added[0].Marker {
		t.Fatalf("added = %+v", added)
	}
	want := map[string][]Version{
		"a": {{Ver: 5, C: 99, Marker: true}},
		"b": {{Ver: 3, C: 30}},
	}
	if got := s.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestApplyDropAdded(t *testing.T) {
	s := New()
	mustLoad(t, s, "a", Version{Ver: 1, C: 10})
	added, err := s.Apply("a", []int64{50}, []uint64{1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Ver != 2 {
		t.Fatalf("added = %+v", added)
	}
	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("key must be empty after dropAdded: %v", got)
	}
}

func mustLoad(t *testing.T, s *Store, key string, v Version) {
	t.Helper()
	if err := s.Load(key, v); err != nil {
		t.Fatalf("Load(%q, %+v): %v", key, v, err)
	}
}
