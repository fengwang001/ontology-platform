package ontology

import (
	"errors"
	"testing"
)

// TestDerivedStaleLifecycle 验证派生状态在链接待处理期间被标记为暂时不可信、
// 内容不被清空，查询时显式携带不可信标注；链接恢复后重新可信。
func TestDerivedStaleLifecycle(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 3, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 3)
	derivedID, err := m.RegisterDerived(ids[2], "aggregation-v1")
	if err != nil {
		t.Fatal(err)
	}
	// 下调使 ids[2] 进入待处理 -> 派生状态变为暂时不可信，但内容保留。
	if err := m.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	v, err := m.QueryDerived(derivedID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stale || v.StaleReason == "" {
		t.Fatalf("derived = %+v, want stale with reason", v)
	}
	if v.Payload != "aggregation-v1" {
		t.Fatalf("payload = %v, want preserved", v.Payload)
	}
	// 上调恢复 -> 派生状态重新可信。
	if err := m.SetLimit("T", DirectionOut, 3); err != nil {
		t.Fatal(err)
	}
	v, err = m.QueryDerived(derivedID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Stale {
		t.Fatalf("derived still stale after restore: %+v", v)
	}
}

// TestDerivedCleanedOnDelete 链接最终删除时派生状态被清理；
// 转为保留时派生状态恢复可信。
func TestDerivedCleanedOnDelete(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 3, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 3)
	d1, _ := m.RegisterDerived(ids[1], "d1")
	d2, _ := m.RegisterDerived(ids[2], "d2")
	if err := m.SetLimit("T", DirectionOut, 1); err != nil {
		t.Fatal(err)
	}
	// ids[2] 最终删除 -> 派生状态被清理。
	if err := m.ResolvePending(ids[2], ResolveDelete); err != nil {
		t.Fatal(err)
	}
	if _, err := m.QueryDerived(d2); !errors.Is(err, ErrDerivedNotFound) {
		t.Fatalf("query cleaned derived: err = %v, want ErrDerivedNotFound", err)
	}
	// ids[1] 转为保留 -> 派生状态恢复可信。
	if err := m.ResolvePending(ids[1], ResolveKeep); err != nil {
		t.Fatal(err)
	}
	v, err := m.QueryDerived(d1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Stale {
		t.Fatalf("derived of kept link still stale: %+v", v)
	}
}

// TestDerivedStaleOnlyWhilePending 派生状态只在链接确实待处理期间不可信：
// 链接在某一方向恢复但另一方向仍待处理时，派生状态保持不可信。
func TestDerivedStaleOnlyWhilePending(t *testing.T) {
	m := NewManager()
	// 两个方向都设上限，使同一链接可能同时受两侧约束。
	if err := m.DefineLinkType("T", "A", "B", 2, 2); err != nil {
		t.Fatal(err)
	}
	m.RegisterObject("s0")
	m.RegisterObject("s1")
	m.RegisterObject("t0")
	m.RegisterObject("t1")
	// l2 同时是 s0 出方向与 t0 入方向上最新的一条，
	// 因而可能在两个方向上分别进入待处理。
	if err := m.CreateLink("T", "l0", "s0", "t1"); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateLink("T", "l1", "s1", "t0"); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateLink("T", "l2", "s0", "t0"); err != nil {
		t.Fatal(err)
	}
	d, _ := m.RegisterDerived("l2", "x")
	// t0 的 in 方向上限 1 -> l2 进入待处理。
	if err := m.SetLimit("T", DirectionIn, 1); err != nil {
		t.Fatal(err)
	}
	v, _ := m.QueryDerived(d)
	if !v.Stale {
		t.Fatal("derived should be stale while link pending")
	}
	// in 方向恢复，但 out 方向下调使 l2 仍待处理。
	if err := m.SetLimit("T", DirectionIn, 2); err != nil {
		t.Fatal(err)
	}
	if err := m.SetLimit("T", DirectionOut, 1); err != nil {
		t.Fatal(err)
	}
	v, _ = m.QueryDerived(d)
	if !v.Stale {
		t.Fatal("derived should remain stale while link pending in another direction")
	}
	if err := m.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	v, _ = m.QueryDerived(d)
	if v.Stale {
		t.Fatal("derived should be trusted after link fully restored")
	}
}
