package rank

// rank 包内部测试：只有这里能读到非导出计数器 cmp，证明比较次数为对数级。
// 计数器绝不经任何导出接口暴露（本文件之外没有任何代码访问 cmp）。

import (
	"math/bits"
	"strconv"
	"testing"
)

// TestComparisonLogarithmic：m 行存活集上「插最大行」「撤首条」两步的比较次数
// 均 ≤ 4*(⌊log2 m⌋+2)，多档 m（100..10000）+ 随机 T；线性扫描不可能通过。
func TestComparisonLogarithmic(t *testing.T) {
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		s, x := New(), uint64(20260924) // LCG 决定 T，确定且散布
		for i := 0; i < m; i++ {
			x = x*6364136223846793005 + 1442695040888963407
			s.Insert(Row{ID: "id" + strconv.Itoa(i), T: int64(x % (1 << 40))})
		}
		mn, _ := s.Min()
		bound := 4 * (bits.Len(uint(m)) + 1) // 4*(⌊log2 m⌋+2)
		s.Insert(Row{ID: "zzz", T: 1 << 62}) // 排序键最大：沿最右脊下行
		if s.cmp > bound {
			t.Fatalf("m=%d insert-largest comparisons=%d > bound=%d", m, s.cmp, bound)
		}
		s.Delete(mn.ID) // 撤回首条：找次早行靠有序结构而非整组扫描
		if s.cmp > bound {
			t.Fatalf("m=%d delete-min comparisons=%d > bound=%d", m, s.cmp, bound)
		}
	}
}
