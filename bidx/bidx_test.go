package bidx

import "testing"

// lcg 确定性伪随机序列（含回退、重复、负值），避免测试依赖 rand。
func lcgSeq(n int, seed int64) []int64 {
	out := make([]int64, n)
	s := seed
	for i := range out {
		s = s*6364136223846793005 + 1442695040888963407
		v := s >> 33
		out[i] = v%201 - 100 // [-100,100]，大量重复与回退
	}
	return out
}

func fill(t *testing.T, n int, seq []int64) *Index {
	t.Helper()
	x := New(n)
	for _, ts := range seq {
		if err := x.Append(ts); err != nil {
			t.Fatalf("Append(%d): %v", ts, err)
		}
	}
	return x
}

// TestSafeOffComparesLog 钉住复杂度：SafeOff 二分比较次数 <= ceil(log2(N+1)) + 2，
// 不随 N 线性增长。直接读非导出字段 lastCompares（同包白盒，不经公开接口）。
func TestSafeOffComparesLog(t *testing.T) {
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		x := fill(t, n, lcgSeq(n, int64(n)+1))
		if _, _, err := x.SafeOff(37); err != nil {
			t.Fatalf("N=%d SafeOff: %v", n, err)
		}
		limit := 0
		for (1 << limit) < n+1 {
			limit++
		}
		limit += 2 // 小常数
		if got := x.lastCompares.Load(); got > int64(limit) {
			t.Errorf("N=%d: compares=%d > ceil(log2(N+1))+2=%d（疑似线性扫描）",
				n, got, limit)
		}
	}
}

// TestPrefixMaxMonotone 钉住不变量 3：任意追加序列下 pm 非递减。
func TestPrefixMaxMonotone(t *testing.T) {
	cases := [][]int64{
		{2, 1, 8, 3, 4, 9, 5, 7},
		{5, 5, 5, 5},
		{-3, -1, -2, 0, -100, 100},
		lcgSeq(1000, 42),
	}
	for _, seq := range cases {
		x := fill(t, len(seq), seq)
		if len(x.pm) != len(seq) {
			t.Fatalf("pm len=%d want %d", len(x.pm), len(seq))
		}
		for i := 1; i < len(x.pm); i++ {
			if x.pm[i] < x.pm[i-1] {
				t.Fatalf("seq=%v: pm[%d]=%d < pm[%d]=%d", seq, i, x.pm[i], i-1, x.pm[i-1])
			}
		}
	}
}
