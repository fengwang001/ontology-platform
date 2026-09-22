package store

import "testing"

// 第 10 条：只读查询不撒谎。连查两次结果完全相同；
// 已回收或从未存在的键查询为零值。
func TestStatsStableAcrossQueries(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1")
	commitKV(t, s, "k", "v2")
	snap, _ := s.Begin()
	defer s.Release(snap)
	s1 := s.Stats()
	s2 := s.Stats()
	if s1 != s2 {
		t.Fatalf("连查两次必须相同: %+v vs %+v", s1, s2)
	}
	if s1.OpenSnapshots != 1 || s1.TotalVersions != 2 {
		t.Fatalf("统计不符合预期: %+v", s1)
	}
	k1, k2 := s.KeyVersions("k"), s.KeyVersions("k")
	if k1 != k2 || k1 != 2 {
		t.Fatalf("键版本数连查应相同且为 2: %d %d", k1, k2)
	}
	if got := s.KeyVersions("absent"); got != 0 {
		t.Fatalf("从未存在的键查询应为零值，得到 %d", got)
	}
	s.Release(snap)
	s.Reclaim()
	if got := s.KeyVersions("k"); got != 1 {
		t.Fatalf("回收后只剩最新版本，应为 1，得到 %d", got)
	}
	snap2, _ := s.Begin()
	defer s.Release(snap2)
	if _, lk := s.ReadAt(snap2, "k"); lk != LookupFound {
		t.Fatalf("回收后最新版本仍应可见")
	}
}

// 查询不推进水位等状态（除显式 Reclaim 外）。
func TestQueriesDoNotAdvanceState(t *testing.T) {
	s := newStore(t, Config{})
	commitKV(t, s, "k", "v1")
	commitKV(t, s, "k", "v2")
	for i := 0; i < 5; i++ {
		s.Stats()
		s.KeyVersions("k")
	}
	st := s.Stats()
	if st.WaterLevel != 0 || st.Examined != 0 {
		t.Fatalf("查询不得推进水位或考察计数: %+v", st)
	}
}
