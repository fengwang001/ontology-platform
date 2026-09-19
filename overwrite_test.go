package ontology

import (
	"reflect"
	"testing"
)

// 后到的分数覆盖先到的分数，并立即体现在排名中。
func TestOverwriteTakesEffectImmediately(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 1)
	sel.Push("b", 2)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("before overwrite: %v", got)
	}
	sel.Push("a", 100)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("after overwrite: %v", got)
	}
	if sel.Len() != 2 {
		t.Fatalf("len must stay 2, got %d", sel.Len())
	}
}

// 覆盖后分数变差到掉出 Top-K：该 ID 必须立即从保留集合消失。
func TestOverwriteWorseDropsOutOfTopK(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 100)
	sel.Push("b", 50)
	sel.Push("a", 1) // a 降为 1，比 b 的 50 差，应掉出

	got := sel.Snapshot()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("a should have dropped out: %+v", got)
	}
}

// Asc 方向下覆盖为更大的分数也应掉出。
func TestOverwriteWorseDropsOutOfTopKAsc(t *testing.T) {
	sel, _ := New(2, Asc)
	sel.Push("a", 1)
	sel.Push("b", 50)
	sel.Push("a", 100) // a 变大，掉出最小的两个

	got := sel.Snapshot()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("a should have dropped out (asc): %+v", got)
	}
}

// Snapshot 中同一 ID 绝不出现两次；反复覆盖也不产生副本。
func TestNoDuplicateIDAfterRepeatedOverwrites(t *testing.T) {
	sel, _ := New(3, Desc)
	scores := []float64{5, 9, 2, 7, 3, 100, -4, 6}
	for i, s := range scores {
		// 一半更新打到固定 ID 上。
		id := "fixed"
		if i%2 == 1 {
			id = "other"
		}
		sel.Push(id, s)
	}
	seen := map[string]int{}
	for _, e := range sel.Snapshot() {
		seen[e.ID]++
	}
	if len(seen) != len(sel.Snapshot()) {
		t.Fatalf("duplicate IDs in snapshot: %v", seen)
	}
}

// 掉出 Top-K 的 ID 以更好分数重新 Push，应当能重新入选。
func TestDroppedIDCanReturn(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 100)
	sel.Push("b", 50)
	sel.Push("a", 1) // a 掉出
	sel.Push("c", 60)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("before return: %v", got)
	}
	sel.Push("a", 70) // a 回来并超过 c
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("after return: %v", got)
	}
}
