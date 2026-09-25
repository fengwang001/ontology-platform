package conn

import "testing"

// TestDispatchO1 证明事件分派是 O(1) 查表命中：逐事件断言检查的转移规则数
// 恰好为 1，m 个事件（合法/非法交替）的总检查数恰好等于 m，而非按
// 「状态 × 事件」双层循环扫描全部 R 条规则。
func TestDispatchO1(t *testing.T) {
	for _, m := range []int{100, 777, 10000} {
		c := New(100)
		for i := 0; i < m; i++ {
			var e Event
			switch i % 3 { // 合法/非法交替
			case 0:
				e = EvData // ESTABLISHED 下合法
			case 1:
				e = EvACK // ESTABLISHED 下非法
			case 2:
				e = EvTick // 非 TIME_WAIT 合法 no-op
			}
			before := c.checks
			c.Apply(e, int64(i))
			if d := c.checks - before; d != 1 {
				t.Fatalf("m=%d event %d: checked %d rules, want exactly 1", m, i, d)
			}
		}
		if c.checks != m {
			t.Fatalf("m=%d: total checks=%d, want exactly %d", m, c.checks, m)
		}
	}
}
