package entry

import (
	"testing"

	"ontology/version"
)

func TestHoleAndNegativeCacheDistinct(t *testing.T) {
	e := New(0)
	if e.State() != Hole || e.Snapshot().Loaded {
		t.Fatal("新条目必须是 Hole 且未加载")
	}
	if !e.BeginFetch(0) {
		t.Fatal("Hole 应允许回源")
	}
	if !e.CommitFetch(Payload{Exists: false, Ver: version.New(1)}, 10, 0) {
		t.Fatal("不存在结果应成功提交为负缓存")
	}
	s := e.Snapshot()
	if s.State != Valid || s.Exists || !s.Loaded {
		t.Fatalf("负缓存状态错误: %+v", s)
	}
}

func TestExpiryLeftClosedRightOpen(t *testing.T) {
	e := New(0)
	e.BeginFetch(0)
	e.CommitFetch(Payload{Exists: true, Data: []byte("a"), Ver: version.New(1)}, 10, 0)
	if e.Expired(9) {
		t.Fatal("now=9 未到期")
	}
	if !e.Expired(10) {
		t.Fatal("now==expire 左闭右开必须算过期")
	}
	if !e.LazyExpire(10) || e.State() != Stale {
		t.Fatal("到期后惰性过期必须进入 Stale")
	}
}

func TestOldInvalidationDropped(t *testing.T) {
	e := New(0)
	e.BeginFetch(0)
	e.CommitFetch(Payload{Exists: true, Ver: version.New(3)}, 100, 0)
	if e.ApplyInvalidate(version.New(2)) {
		t.Fatal("旧版本通知必须被丢弃")
	}
	if e.State() != Valid || e.Invalidations() != 0 {
		t.Fatal("旧通知不得改变任何状态")
	}
	if !e.ApplyInvalidate(version.New(3)) == false {
		t.Fatal("同版本水位通知必须幂等丢弃")
	}
	if !e.ApplyInvalidate(version.New(4)) || e.State() != Stale {
		t.Fatal("新版本通知必须生效")
	}
	if e.ApplyInvalidate(version.New(4)) {
		t.Fatal("重复通知不得重复计数")
	}
	if e.Invalidations() != 1 {
		t.Fatalf("失效次数=%d, want 1", e.Invalidations())
	}
}

func TestFetchStaleResultDiscarded(t *testing.T) {
	e := New(0)
	e.BeginFetch(0)
	e.CommitFetch(Payload{Exists: true, Data: []byte("old"), Ver: version.New(1)}, 100, 0)
	// v4 失效使数据转为 Stale；读触发第二次回源
	if !e.ApplyInvalidate(version.New(4)) {
		t.Fatal("v4 通知应使有效数据失效")
	}
	if !e.BeginFetch(5) {
		t.Fatal("Stale 应允许回源")
	}
	// 回源进行中又收到 v5，回源却返回 v4 旧值
	if !e.NoteFetchingInvalidate(version.New(5)) {
		t.Fatal("回源中高版本通知必须记录")
	}
	if e.CommitFetch(Payload{Exists: true, Data: []byte("stale"), Ver: version.New(4)}, 100, 6) {
		t.Fatal("过期结果必须被拒绝提交")
	}
	s := e.Snapshot()
	if s.State != Stale || string(s.Data) != "old" {
		t.Fatalf("旧结果不得写入, state=%s data=%q", s.State, s.Data)
	}
	if s.Invalid != 1 {
		t.Fatalf("失效次数=%d, want 1（仅 v4 作废了已落盘数据；v5 不作额外计数）", s.Invalid)
	}
	// 下一次回源拿到 v5 新鲜结果后必须可以正常提交
	if !e.BeginFetch(7) {
		t.Fatal("丢弃旧结果后必须可立即重新回源")
	}
	if !e.CommitFetch(Payload{Exists: true, Data: []byte("new"), Ver: version.New(5)}, 100, 8) {
		t.Fatal("v5 新鲜结果必须提交成功")
	}
	if string(e.Snapshot().Data) != "new" || e.State() != Valid {
		t.Fatal("重新回源后缓存应为新值")
	}
}

func TestFetchFailureLeavesRefetchable(t *testing.T) {
	e := New(0)
	e.BeginFetch(0)
	e.AbortFetch()
	if e.State() != Hole {
		t.Fatal("Hole 回源失败必须回到 Hole（可重新回源）")
	}
	if !e.BeginFetch(0) {
		t.Fatal("失败后必须能立即重新回源")
	}
	e.AbortFetch()
	if e.Fetches() != 2 {
		t.Fatalf("重试必须真正再回源, fetches=%d", e.Fetches())
	}
}
