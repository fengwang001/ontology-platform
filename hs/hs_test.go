package hs

import (
	"strconv"
	"testing"
)

// TestProbedConstant 证明按 src 定位半开连接是 O(1) 映射查找：
// 不同规模的半开表下，单次查询检查的半开连接个数不随 h 增长。
func TestProbedConstant(t *testing.T) {
	prev := -1
	for _, h := range []int{100, 500, 1000, 5000, 10000} {
		s := New()
		for i := 0; i < h; i++ {
			s.PutHalf("src-"+strconv.Itoa(i), Conn{ClientISN: int64(i), ServerISN: s.Alloc()})
		}
		if _, ok := s.LookupHalf("src-" + strconv.Itoa(h/2)); !ok {
			t.Fatalf("h=%d: 目标 src 不在半开表", h)
		}
		if s.probed > 2 {
			t.Fatalf("h=%d: 检查了 %d 个半开连接，随规模线性增长", h, s.probed)
		}
		if prev >= 0 && s.probed != prev {
			t.Fatalf("h=%d: 检查个数 %d 与上一档 %d 不同，随 h 增长", h, s.probed, prev)
		}
		prev = s.probed
	}
}

// TestAllocMonotonic 钉住 serverISN 单调递增、绝不重复。
func TestAllocMonotonic(t *testing.T) {
	s := New()
	seen := map[int64]bool{}
	for i := int64(0); i < 1000; i++ {
		isn := s.Alloc()
		if isn != i || seen[isn] {
			t.Fatalf("第 %d 次 Alloc = %d，不单调或重复", i, isn)
		}
		seen[isn] = true
	}
	if s.NextISN() != 1000 {
		t.Fatalf("NextISN = %d，期望 1000", s.NextISN())
	}
}
