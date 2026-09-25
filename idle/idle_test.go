package idle

import "testing"

// TestLateCheckCountConstant 证明迟到判定不回扫历史：
// 先喂 m 个事件，再喂 1 个并判定其是否迟到，
// 检查个数增量是不随 m 增长的小常数（本实现恒为 1，只比当前水位）。
func TestLateCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := NewStream(3, 5)
		for i := 0; i < m; i++ {
			s.Feed("k", int64(10+i), int64(i))
		}
		before := s.lateChecks
		s.Feed("candidate", int64(10+m), int64(m)) // 触发一次迟到判定
		if got := s.lateChecks - before; got > 2 {
			t.Fatalf("m=%d: 单次迟到判定检查了 %d 个值，随历史增长", m, got)
		}
	}
}

// TestLateCheckCountsOnlyDecisions 计数器等于判定次数而非事件个数。
func TestLateCheckCountsOnlyDecisions(t *testing.T) {
	s := NewStream(3, 5)
	for i := 0; i < 1000; i++ {
		s.Feed("k", int64(10+i), int64(i))
	}
	if s.lateChecks != 1000 { // 1000 次判定 x 每次 1 个值，而非 O(m^2)
		t.Fatalf("lateChecks=%d, want 1000", s.lateChecks)
	}
}
