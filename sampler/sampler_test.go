package sampler

import "testing"

// TestSampleAccessCounter 证明 Sample 只访问已存的 k 个元素，
// 访问计数不随已见元素总数 m 增长（非导出字段，仅包内测试可读）。
func TestSampleAccessCounter(t *testing.T) {
	const k = 10
	rng := func(i int) int { return int((uint(i)*2654435761)>>16)%i + 1 }
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s, err := New(k, rng)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := s.Feed("x"); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(s.Sample()); got != k {
			t.Fatalf("m=%d: |sample|=%d, want %d", m, got, k)
		}
		if s.lastAccess != k {
			t.Fatalf("m=%d: lastAccess=%d, want %d", m, s.lastAccess, k)
		}
		_ = s.Sample() // 再调一次仍恒为 k
		if s.lastAccess != k {
			t.Fatalf("m=%d: second Sample lastAccess=%d, want %d", m, s.lastAccess, k)
		}
	}
}
