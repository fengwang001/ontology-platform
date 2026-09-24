package agg

import (
	"strconv"
	"testing"

	"ontology/gset"
)

// TestProbeCountConstant 直接读非导出字段 probe（不经过任何导出接口）：
// 命中已有键时检查过的现存键数恒为 1，不随现存键总数 m 线性增长；
// 未命中时为 0。多档 m 用循环生成。
func TestProbeCountConstant(t *testing.T) {
	g, _ := gset.NewGroup([]int{0})
	for _, m := range []int{100, 1000, 10000} {
		tab := New([]gset.Group{g})
		for i := 0; i < m; i++ {
			f := [gset.NumDims]string{"k" + strconv.Itoa(i), "b", "c"}
			if err := tab.Apply(f, 1); err != nil {
				t.Fatalf("m=%d seed: %v", m, err)
			}
		}
		if err := tab.Apply([gset.NumDims]string{"k0", "b", "c"}, 1); err != nil {
			t.Fatal(err)
		}
		if tab.probe != 1 { // 每组恰好一次哈希命中即定位
			t.Fatalf("m=%d hit probe=%d, want 1 (must not grow with m)", m, tab.probe)
		}
		if err := tab.Apply([gset.NumDims]string{"brand-new", "b", "c"}, 1); err != nil {
			t.Fatal(err)
		}
		if tab.probe != 0 { // 未命中也不扫描现存键全集
			t.Fatalf("m=%d miss probe=%d, want 0", m, tab.probe)
		}
	}
}
