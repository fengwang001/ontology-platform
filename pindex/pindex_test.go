package pindex

import "testing"

// 谓词边界：50 左闭命中，49 不命中。
func TestHitBoundary(t *testing.T) {
	cases := []struct {
		score int
		hit   bool
	}{{0, false}, {49, false}, {50, true}, {51, true}, {100, true}}
	for _, c := range cases {
		if Hit(c.score) != c.hit {
			t.Errorf("Hit(%d)=%v, want %v", c.score, !c.hit, c.hit)
		}
	}
}

// Lookup 为返回结果检查过的行数不得随全表行数 m 线性增长：
// m 档行的 Key 互不相同且都命中，查其中一个 Key，检查行数 ≤ 小常数，
// 证明 Lookup 按 Key 直接定位而非扫全表过滤。计数器是非导出字段，
// 只有同包测试能读到它，公开接口不暴露。
func TestLookupExaminedRowsBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		ix := New()
		for i := 0; i < m; i++ {
			ix.Add(i, i) // key=id=i：每个 Key 下恰好 1 行，全部命中
		}
		ix.Lookup(m / 2)
		if ix.examined > 2 {
			t.Fatalf("m=%d: Lookup examined %d rows, want <= 2 (direct key locate, not full scan)", m, ix.examined)
		}
		ix.Lookup(m + 1) // 不存在的 Key
		if ix.examined != 0 {
			t.Fatalf("m=%d: missing key examined %d rows, want 0", m, ix.examined)
		}
	}
	// 检查行数只等于该 Key 下的结果条数，与 m 无关。
	ix := New()
	for i := 0; i < 10000; i++ {
		ix.Add(i+1000, i) // key 与 7 错开，避免污染待查 Key
	}
	for _, id := range []int{10001, 10002, 10003, 10004, 10005} {
		ix.Add(7, id)
	}
	if got := ix.Lookup(7); len(got) != 5 || ix.examined != 5 {
		t.Fatalf("Lookup(7)=%v examined=%d, want 5/5", got, ix.examined)
	}
}
