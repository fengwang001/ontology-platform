package plan

import (
	"strconv"
	"testing"

	"ontology/ev"
)

// 复杂度约束：预演对照可见视图的查找次数 ≤ 批内事件数 + 小常数，与视图大小 m 无关。
func TestLookupCountIndependentOfViewSize(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		view := map[string]string{}
		for i := 0; i < m; i++ {
			view[strconv.Itoa(i)] = "v"
		}
		var r Rehearser
		batch := []ev.Event{ev.Put("brand-new-key", "v", nil)}
		if _, err := r.Rehearse(batch, func(k string) (string, bool) {
			s, ok := view[k]
			return s, ok
		}); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if r.lookups > len(batch)+1 { // 非导出计数器：白盒断言
			t.Fatalf("m=%d: lookups=%d exceeds batch size + const", m, r.lookups)
		}
	}
}

// 同批内重复触碰同键时，后续事件命中 overlay，不再查视图。
func TestLookupCountOverlayHits(t *testing.T) {
	var r Rehearser
	sa, sb := "a", "b"
	batch := []ev.Event{
		ev.Put("k", "a", nil), ev.Put("k", "b", &sa), ev.Del("k", &sb), ev.Put("k", "c", nil),
	}
	if _, err := r.Rehearse(batch, func(string) (string, bool) { return "", false }); err != nil {
		t.Fatal(err)
	}
	if r.lookups != 1 { // 仅第 1 条查视图，其余全部命中批内累积状态
		t.Fatalf("lookups=%d, want 1", r.lookups)
	}
}
