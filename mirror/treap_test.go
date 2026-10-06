package mirror

import (
	"math/rand"
	"testing"
)

func TestBlockSetOrderedOps(t *testing.T) {
	var s blockSet
	r := rand.New(rand.NewSource(1))
	keys := r.Perm(1000)
	for _, k := range keys {
		s.insert(k)
	}
	for _, k := range keys { // 重复插入为空操作
		s.insert(k)
	}
	if s.len() != 1000 {
		t.Fatalf("长度应为 1000，实际 %d", s.len())
	}
	got := s.ascending()
	for i, k := range got {
		if k != i {
			t.Fatalf("升序遍历第 %d 项应为 %d，实际 %d", i, i, k)
		}
	}
	for _, k := range keys[:500] {
		s.remove(k)
	}
	if s.len() != 500 {
		t.Fatalf("删除后长度应为 500，实际 %d", s.len())
	}
	prev := -1
	for s.len() > 0 {
		k, ok := s.popMin()
		if !ok || k <= prev {
			t.Fatalf("popMin 应严格升序弹出，上一值 %d，当前 %d, ok=%v", prev, k, ok)
		}
		prev = k
	}
	if _, ok := s.popMin(); ok {
		t.Fatalf("空集合 popMin 应返回 false")
	}
}

func TestBlockSetDeterministic(t *testing.T) {
	build := func(order []int) *blockSet {
		s := &blockSet{}
		for _, k := range order {
			s.insert(k)
		}
		return s
	}
	a := build([]int{5, 3, 9, 1, 7})
	b := build([]int{9, 7, 5, 3, 1})
	for i := 0; i < 5; i++ {
		ka, _ := a.popMin()
		kb, _ := b.popMin()
		if ka != kb {
			t.Fatalf("不同插入顺序应得到相同弹出序列，第 %d 项 %d != %d", i, ka, kb)
		}
	}
}
