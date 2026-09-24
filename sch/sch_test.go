package sch

import (
	"fmt"
	"testing"
)

// TestDelScanConstant 证明「是否存在引用子行」的判定走引用计数而非扫描子表：
// 无论挂多少个子行，ParentDeletable 的扫描计数都不随 m 增长（恒为小常数）。
// 计数器 delScan 是非导出字段，本测试与实现同包直接读字段，不经任何导出接口。
func TestDelScanConstant(t *testing.T) {
	const limit = 4 // 与 m 无关的小常数上界
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			s := New()
			s.AddParent("hot")  // 被引用
			s.AddParent("cold") // 无引用
			for i := 0; i < m; i++ {
				s.AddChild(fmt.Sprintf("k%d", i), "hot")
			}
			// 被引用父行的判定（对应 fk.PDel 被拒路径）
			exists, inUse := s.ParentDeletable("hot")
			if !exists || !inUse {
				t.Fatalf("hot: got exists=%v inUse=%v", exists, inUse)
			}
			if s.delScan > limit {
				t.Fatalf("referenced check scanned %d rows, m=%d", s.delScan, m)
			}
			// 无引用父行的判定（对应 fk.PDel 成功路径）
			exists, inUse = s.ParentDeletable("cold")
			if !exists || inUse {
				t.Fatalf("cold: got exists=%v inUse=%v", exists, inUse)
			}
			if s.delScan > limit {
				t.Fatalf("unreferenced check scanned %d rows, m=%d", s.delScan, m)
			}
			s.DelParent("cold")
			if s.HasParent("cold") {
				t.Fatal("cold should be deleted")
			}
		})
	}
}
