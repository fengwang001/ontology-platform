package wmgr

import (
	"fmt"
	"testing"
)

// GC 为找可回收窗口而检查的窗口数，只等于 purged 集合大小，
// 与总窗口数 m 无关（非导出计数器 lastGCChecked 仅本包内测试可读）。
func TestGCCheckCountIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			mg := New(10, int64(m)+1)
			for i := 0; i < m; i++ {
				if err := mg.Ingest(fmt.Sprintf("a%d", i), 1); err != nil {
					t.Fatal(err)
				}
			}
			if err := mg.Ingest("p", 1); err != nil {
				t.Fatal(err)
			}
			if err := mg.Purge("p"); err != nil {
				t.Fatal(err)
			}
			mg.GC()
			if mg.lastGCChecked != 1 {
				t.Fatalf("m=%d: GC 检查了 %d 个窗口, 期望常数 1", m, mg.lastGCChecked)
			}
			if _, _, _, ok := mg.Snapshot("p"); ok {
				t.Fatalf("m=%d: purged 窗口未被 GC 删除", m)
			}
			if _, _, _, ok := mg.Snapshot("a0"); !ok {
				t.Fatalf("m=%d: active 窗口被 GC 误删", m)
			}
		})
	}
}
