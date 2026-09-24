package route

import "testing"

// TestPrefixTotalComplexity 钉住第四节：PrefixTotal 经各分区前缀和数组
// O(1) 取前缀，检查的分区个数不随每分区事件数 m 线性增长，恒不超过 P。
func TestPrefixTotalComplexity(t *testing.T) {
	const P = 8
	for _, m := range []int{100, 1000, 10000} {
		r := NewRouter(P)
		for i := 0; i < m; i++ {
			for p := 0; p < P; p++ {
				if err := r.Append(p, int64(i), int64(i+p)); err != nil {
					t.Fatalf("m=%d append: %v", m, err)
				}
			}
		}
		_ = r.PrefixTotal()
		if got := r.prefixChecks.Load(); got > P {
			t.Fatalf("m=%d: prefixChecks=%d, want <= %d (与 m 无关)", m, got, P)
		}
		if got := r.prefixChecks.Load(); got != P {
			t.Fatalf("m=%d: prefixChecks=%d, want exactly %d", m, got, P)
		}
	}
}
