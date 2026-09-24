package hist

import "testing"

// 表驱动：At 的可见性边界（含等号）与空链行为。
func TestAtVisibility(t *testing.T) {
	var c Chain
	c.Append(2, "v2")
	c.Append(5, "v5")
	c.Append(9, "v9")
	cases := []struct {
		s    int64
		want Val
		ok   bool
	}{
		{0, nil, false}, {1, nil, false}, // 链头之前不可见
		{2, "v2", true}, // 等号成立即可见
		{4, "v2", true}, {5, "v5", true}, {8, "v5", true},
		{9, "v9", true}, {100, "v9", true}, // 越过链尾收敛到最新
	}
	for _, tc := range cases {
		if got, ok := c.At(tc.s); got != tc.want || ok != tc.ok {
			t.Errorf("At(%d) = (%v,%v), want (%v,%v)", tc.s, got, ok, tc.want, tc.ok)
		}
	}
	var empty Chain
	if _, ok := empty.At(5); ok {
		t.Error("empty chain must not be visible")
	}
}

// 表驱动：Compact 的基线语义。
func TestCompactBaseline(t *testing.T) {
	cases := []struct {
		name string
		upto int64
		want []Val //  Compact 后链上剩余版本（按 Seq 升序）
	}{
		{"全部降为基线", 9, []Val{"v9"}},
		{"中间收基线", 5, []Val{"v5", "v9"}},
		{"基线之前全丢", 3, []Val{"v2", "v5", "v9"}},
		{"无版本可收", 1, []Val{"v2", "v5", "v9"}},
	}
	for _, tc := range cases {
		var c Chain
		c.Append(2, "v2")
		c.Append(5, "v5")
		c.Append(9, "v9")
		c.Compact(tc.upto)
		if len(c.vals) != len(tc.want) {
			t.Fatalf("%s: len=%d, want %d", tc.name, len(c.vals), len(tc.want))
		}
		for i, w := range tc.want {
			if c.vals[i] != w {
				t.Errorf("%s: vals[%d]=%v, want %v", tc.name, i, c.vals[i], w)
			}
		}
	}
}

// 复杂度钉住：m 取多档，At 到最新位点检查个数不超过与 m 无关的小常数。
// 计数器 checked 是非导出字段，只能在本包内读取，公开接口不暴露。
func TestAtCheckedConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var hot, other Chain
		for i := 1; i <= m; i++ {
			hot.Append(int64(2*i-1), i) // 其他 key 的写入穿插其中
			other.Append(int64(2*i), i)
		}
		if _, ok := hot.At(int64(2*m - 1)); !ok {
			t.Fatalf("m=%d: latest must be visible", m)
		}
		if hot.checked.Load() > 4 {
			t.Errorf("m=%d: checked=%d, want <=4 (constant)", m, hot.checked.Load())
		}
	}
}
