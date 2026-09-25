package sqrt

import (
	"testing"
)

// TestNaiveAgreement 钉住不变量 2：与「从 0 逐个平方扫到超过 n」的朴素参照一致。
func TestNaiveAgreement(t *testing.T) {
	for n := int64(0); n <= 1_000_000; n += 101 {
		var k int64
		for (k+1)*(k+1) <= n {
			k++
		}
		if r, err := Isqrt(n); err != nil || r != k {
			t.Fatalf("isqrt(%d) = %d, %v; naive wants %d", n, r, err, k)
		}
	}
}

// TestSquareBoundaries 钉住不变量 3：k²→k 精确，k²−1→k−1，k²+1→k。
func TestSquareBoundaries(t *testing.T) {
	for _, k := range []int64{1, 2, 3, 100, 1000, 67108864, 67108865, 3037000499} {
		cases := []struct{ n, want int64 }{
			{k * k, k},
			{k*k - 1, k - 1},
			{k*k + 1, k},
		}
		for _, c := range cases {
			if r, err := Isqrt(c.n); err != nil || r != c.want {
				t.Errorf("isqrt(%d) = %d, %v; want %d", c.n, r, err, c.want)
			}
		}
	}
}

// TestNewtonIterationBounded 钉住对数收敛：m²/m²±1 的迭代次数 ≤40，与规模无关。
// 白盒直读非导出字段 lastIters；公开接口不暴露该计数器。
func TestNewtonIterationBounded(t *testing.T) {
	for m := int64(100); m <= 10000; m += 111 {
		for _, d := range []int64{-1, 0, 1} {
			if _, err := Isqrt(m*m + d); err != nil {
				t.Fatalf("m=%d d=%d: %v", m, d, err)
			}
			if got := lastIters.Load(); got > 40 {
				t.Errorf("m=%d d=%d: %d iterations, want <= 40", m, d, got)
			}
		}
	}
}
