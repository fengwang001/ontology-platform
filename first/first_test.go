package first

import (
	"math/rand"
	"testing"

	"ontology/evt"
)

// TestHeapComparisonLogarithmic 钉住复杂度：多档 m 下，先以随机到达顺序加入
// m 个 TS 各不相同的事件，再 Add 一个新事件，断言堆上比较次数不超过
// 2·⌈log2 m⌉+2 —— 沿堆高调整而非线性扫描。直接读非导出字段 lastCmp。
func TestHeapComparisonLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet()
		r := rand.New(rand.NewSource(int64(m)))
		for _, p := range r.Perm(m) { // TS 0..m-1 各一次，随机顺序
			s.Add(evt.Event{Key: "k", TS: int64(p)})
		}
		bound := 2*ceilLog2(m) + 2
		s.Add(evt.Event{Key: "zzz", TS: int64(m)}) // 新最大值：上浮即停
		if s.lastCmp > bound {
			t.Fatalf("m=%d new-max comparisons %d > bound %d", m, s.lastCmp, bound)
		}
		s.Add(evt.Event{Key: "aaa", TS: -1}) // 新最小值：沿整个堆高上浮
		if s.lastCmp > bound {
			t.Fatalf("m=%d new-min comparisons %d > bound %d", m, s.lastCmp, bound)
		}
	}
}

// TestFirstSetPromotionAndDup 在 first 层直接核对次首提升与多重集不去重。
func TestFirstSetPromotionAndDup(t *testing.T) {
	s := NewSet()
	add := func(k string, ts int64) { s.Add(evt.Event{Key: k, TS: ts}) }
	firstIs := func(k string, ts int64) bool {
		e, ok := s.First()
		return ok && e.Key == k && e.TS == ts
	}
	add("b", 5)
	add("a", 5) // 并列：Key 升序
	if !firstIs("a", 5) {
		t.Fatal("tie first should be a@5")
	}
	add("a", 3)
	add("a", 3) // 重复出现
	if !firstIs("a", 3) {
		t.Fatal("after dup adds first should be a@3")
	}
	s.Remove(evt.Event{Key: "a", TS: 3}) // 撤一次，仍剩一次
	if !firstIs("a", 3) {
		t.Fatal("one a@3 remains, first should still be a@3")
	}
	s.Remove(evt.Event{Key: "a", TS: 3}) // 最后一个 a@3 => 提升
	if !firstIs("a", 5) {
		t.Fatal("after last a@3 removed, first should promote to a@5")
	}
}
