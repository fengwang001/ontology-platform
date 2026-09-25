package hist

import "testing"

// TestInsertCheckedBucketsConstant 证明 Insert 用桶号直接定位（map/算术），
// 而不是扫全部分桶：已分配 m 个桶后再 Insert 一个落入全新桶的值，
// 为定位目标桶检查过的桶个数不随 m 线性增长（不超过小常数）。
func TestInsertCheckedBucketsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := New(1, 0)
		for i := 0; i < m; i++ {
			h.Insert(i * 1000) // 彼此相距很远，铺出 m 个已分配桶
		}
		h.Insert(-1) // 落入全新桶
		if h.checked > 2 {
			t.Fatalf("m=%d: checked=%d, 随 m 增长，疑似扫描分桶", m, h.checked)
		}
	}
}
