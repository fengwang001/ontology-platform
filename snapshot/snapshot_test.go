package snapshot

import (
	"bytes"
	"slices"
	"testing"
)

// 字节级确定性：等价状态（无论 map 插入顺序）编码逐字节相同，属性名有序。
func TestFreezeDeterministic(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]any
	}{
		{"标量", map[string]any{"x": 1, "y": "s", "z": true}, map[string]any{"z": true, "y": "s", "x": 1}},
		{"嵌套map", map[string]any{"m": map[string]any{"b": 2, "a": 1}}, map[string]any{"m": map[string]any{"a": 1, "b": 2}}},
		{"切片", map[string]any{"l": []any{1, "a"}}, map[string]any{"l": []any{1, "a"}}},
		{"空属性", map[string]any{}, map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sa, sb := Freeze("T", tc.a), Freeze("T", tc.b)
			if !bytes.Equal(sa.Bytes(), sb.Bytes()) {
				t.Fatalf("等价状态编码不同:\n%s\nvs\n%s", sa.Bytes(), sb.Bytes())
			}
			if !slices.IsSorted(sa.Keys()) {
				t.Fatalf("属性名未排序: %v", sa.Keys())
			}
		})
	}
	// 不同状态编码必须不同。
	if bytes.Equal(Freeze("T", map[string]any{"x": 1}).Bytes(), Freeze("T", map[string]any{"x": 2}).Bytes()) {
		t.Fatal("不同状态编码相同")
	}
	if bytes.Equal(Freeze("A", map[string]any{"x": 1}).Bytes(), Freeze("B", map[string]any{"x": 1}).Bytes()) {
		t.Fatal("不同类型的编码相同")
	}
}

// 只读契约：冻结后源数据被改、快照被反复读取，快照字节始终不变。
func TestSnapshotImmutable(t *testing.T) {
	cases := []struct {
		name   string
		attrs  map[string]any
		mutate func(map[string]any)
	}{
		{"改标量", map[string]any{"n": 1}, func(m map[string]any) { m["n"] = 99 }},
		{"加属性", map[string]any{"n": 1}, func(m map[string]any) { m["new"] = 1 }},
		{"改嵌套map", map[string]any{"m": map[string]any{"a": 1}}, func(m map[string]any) { m["m"].(map[string]any)["a"] = 2 }},
		{"改切片", map[string]any{"l": []any{1}}, func(m map[string]any) { m["l"].([]any)[0] = 9 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Freeze("T", tc.attrs)
			before := s.Bytes()
			tc.mutate(tc.attrs)
			for i := 0; i < 3; i++ {
				_, _ = s.Get("n")
				_ = s.Keys()
			}
			if !bytes.Equal(before, s.Bytes()) {
				t.Fatalf("快照被源数据改动影响:\n%s\nvs\n%s", before, s.Bytes())
			}
			if s.Reads() == 0 {
				t.Fatal("读计数器未工作")
			}
		})
	}
}
