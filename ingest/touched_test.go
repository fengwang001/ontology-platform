package ingest

import (
	"fmt"
	"testing"

	"ontology/mapping"
)

// TestTouchedIndependentOfMappingSize 一次 Index 触碰的映射节点数
// 不超过文档中的路径节点数加 1，与映射已有字段总数无关。
func TestTouchedIndependentOfMappingSize(t *testing.T) {
	doc := map[string]any{
		"a": map[string]any{"b": int64(1), "c": "x"},
		"d": []any{int64(1), int64(2)},
		"e": nil, // nil 不触碰映射
	}
	// 文档路径节点：a、a.b、a.c、d 共 4 个（e 为 nil 不计）。
	const docPathNodes = 4

	touched := make([]int, 0, 2)
	for _, n := range []int{10, 10000} {
		eng, err := New(mapping.True, n+docPathNodes)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := eng.PutMapping(fmt.Sprintf("f%05d", i), mapping.Long); err != nil {
				t.Fatal(err)
			}
		}
		eng.m.ResetTouched()
		if _, _, _, err := eng.Index("id", doc); err != nil {
			t.Fatal(err)
		}
		got := eng.m.Touched()
		t.Logf("已有字段 %d 个时 Index 触碰映射节点 %d 个（上限 %d）", n, got, docPathNodes+1)
		if got > docPathNodes+1 {
			t.Fatalf("已有 %d 字段：touched=%d 超过 文档路径节点数+1=%d", n, got, docPathNodes+1)
		}
		touched = append(touched, got)
	}
	if touched[0] != touched[1] {
		t.Fatalf("touched 应与已有字段总数无关: 10 字段=%d, 10000 字段=%d", touched[0], touched[1])
	}
}
