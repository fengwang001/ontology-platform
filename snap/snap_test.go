package snap

import (
	"math/bits"
	"testing"
)

// 大 m 下 Read 检查的历史条目个数不随 m 线性增长（二分定位，O(log m)）。
func TestReadChecksSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			s.Write("k", int64(i))
		}
		snapID := s.Snapshot() // 在末尾再写一批，让目标版本落在历史中段
		for i := 0; i < m; i++ {
			s.Write("k", int64(i))
		}
		before := s.checked
		if got := s.Read(snapID, "k"); got != int64(m-1) {
			t.Fatalf("m=%d: Read=%d, want %d", m, got, m-1)
		}
		checked := s.checked - before
		limit := bits.Len(uint(2*m)) + 2 // O(log m) 加小常数
		if checked > limit {
			t.Fatalf("m=%d: checked=%d > %d，疑似线性扫描", m, checked, limit)
		}
		if checked > m/10 { // 显式断言不随 m 线性增长
			t.Fatalf("m=%d: checked=%d 随规模线性增长", m, checked)
		}
	}
}

// 边界：空历史读 0；ver==snap 取等命中。
func TestReadBoundary(t *testing.T) {
	s := New()
	if got := s.Read(0, "x"); got != 0 {
		t.Fatalf("空历史读=%d, want 0", got)
	}
	s.Write("x", 7) // ver=1
	snapID := s.Snapshot()
	s.Write("x", 9) // ver=2
	if got := s.Read(snapID, "x"); got != 7 {
		t.Fatalf("边界读=%d, want 7", got)
	}
	if got := s.ReadCurrent("x"); got != 9 {
		t.Fatalf("ReadCurrent=%d, want 9", got)
	}
}
