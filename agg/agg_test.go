package agg

import (
	"fmt"
	"testing"

	"ontology/grp"
)

// 白盒：包内直接读非导出字段 lastChecks，断言定位检查个数不随 m 增长。
func TestLocateChecksBounded(t *testing.T) {
	const bound = 4 // 与 m 无关的小常数
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		mm := New()
		batch := map[grp.Key]int64{}
		for i := 0; i < m; i++ {
			s := fmt.Sprintf("g%05d", i)
			batch[grp.Of(&s)] = 1
		}
		if err := mm.ApplyBatch(batch); err != nil {
			t.Fatalf("m=%d: 建组失败 %v", m, err)
		}
		hit := "g00000" // 已存在的组
		if err := mm.ApplyBatch(map[grp.Key]int64{grp.Of(&hit): 1}); err != nil {
			t.Fatalf("m=%d: 增量失败 %v", m, err)
		}
		if mm.lastChecks > bound {
			t.Fatalf("m=%d: 定位检查了 %d 个组，超过常数界 %d（疑似整表扫描）",
				m, mm.lastChecks, bound)
		}
	}
}
