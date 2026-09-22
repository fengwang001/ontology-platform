package ontology

import (
	"math"
	"testing"
)

// TestBoundaryOwnership 咬死四类边界样本的桶号：
// lo 进第 0 桶；内部边界进右边那个桶；hi 计入上溢；hi 的前一个可表示数进最后一桶。
func TestBoundaryOwnership(t *testing.T) {
	cases := []struct {
		name      string
		lo, hi    float64
		n         int
		boundaryK int // 内部边界的桶号（边界 k 应进第 k 桶）
	}{
		{"0,1,3", 0, 1, 3, 1},
		{"0,1,9", 0, 1, 9, 7},
		{"-1,2,7", -1, 2, 7, 3},
		{"neg range", -5, -4, 10, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHistogram(tc.lo, tc.hi, tc.n)
			if err != nil {
				t.Fatalf("NewHistogram: %v", err)
			}
			w := (tc.hi - tc.lo) / float64(tc.n)

			// lo（以及 -0.0）必须进第 0 桶。
			if got := h.indexFor(tc.lo); got != 0 {
				t.Fatalf("x=lo: want bucket 0, got %d", got)
			}
			if got := h.indexFor(math.Copysign(0, -1)); tc.lo == 0 && got != 0 {
				t.Fatalf("x=-0.0 with lo=0: want bucket 0, got %d", got)
			}

			// 内部边界 lo+k*w 必须进右边的第 k 桶。
			x := tc.lo + float64(tc.boundaryK)*w
			if got := h.indexFor(x); got != tc.boundaryK {
				t.Fatalf("x=internal boundary %.20g: want bucket %d, got %d",
					x, tc.boundaryK, got)
			}

			// hi 本身必须是上溢（返回 n），而不是最后一桶。
			if got := h.indexFor(tc.hi); got != tc.n {
				t.Fatalf("x=hi: want overflow index %d, got %d", tc.n, got)
			}

			// hi 的前一个可表示浮点数必须进最后一桶。
			pred := math.Nextafter(tc.hi, math.Inf(-1))
			if got := h.indexFor(pred); got != tc.n-1 {
				t.Fatalf("x=nextafter(hi,-Inf): want last bucket %d, got %d",
					tc.n-1, got)
			}
		})
	}
}

// TestNaiveAlgorithmExposure 用一组会暴露 int((x-lo)/w) 舍入问题的
// (lo,hi,n,x) 组合，逐个断言校正后桶号正确，且天真算法确实算错。
func TestNaiveAlgorithmExposure(t *testing.T) {
	type tc struct {
		lo, hi float64
		n, k   int // x = lo + k*w，应进第 k 桶
	}
	cases := []tc{
		{0, 1, 3, 1},  // n 不整除区间宽度
		{-1, 2, 7, 1}, // 宽度 3、n=7 不整除
		{0, 1, 9, 7},  // 天真算法把第 7 条边界算进第 6 桶
		{0, 1, 11, 3},
		{-5, -4, 10, 1},
		{0, 1, 22, 6},
	}
	exposed := 0
	for _, c := range cases {
		h, err := NewHistogram(c.lo, c.hi, c.n)
		if err != nil {
			t.Fatalf("NewHistogram: %v", err)
		}
		x := c.lo + float64(c.k)*h.w

		naive := NaiveIndex(c.lo, h.w, c.n, x)
		if got := h.indexFor(x); got != c.k {
			t.Fatalf("case (%v,%v,%d) x=%.20g: 校正后 want %d, got %d",
				c.lo, c.hi, c.n, x, c.k, got)
		}
		if naive != c.k {
			t.Logf("天真算法暴露: (%v,%v,%d) x=%.20g naive=%d, 校正后=%d",
				c.lo, c.hi, c.n, x, naive, c.k)
			exposed++
		}
	}
	if exposed == 0 {
		t.Fatal("没有任何组合暴露天真算法，测试表需要调整")
	}
}

// TestThirdsLiterals 断言 lo=0,hi=1,n=3 下 1/3 进第 1 桶、2/3 进第 2 桶。
func TestThirdsLiterals(t *testing.T) {
	h, _ := NewHistogram(0, 1, 3)
	if got := h.indexFor(1.0 / 3.0); got != 1 {
		t.Fatalf("1/3: want bucket 1, got %d", got)
	}
	if got := h.indexFor(2.0 / 3.0); got != 2 {
		t.Fatalf("2/3: want bucket 2, got %d", got)
	}
}

// TestOutOfRange 覆盖区间外样本的下标。
func TestOutOfRange(t *testing.T) {
	h, _ := NewHistogram(0, 1, 3)
	if got := h.indexFor(math.Inf(-1)); got != -1 {
		t.Fatalf("-Inf: want -1, got %d", got)
	}
	if got := h.indexFor(math.Inf(1)); got != 3 {
		t.Fatalf("+Inf: want 3(over), got %d", got)
	}
	if got := h.indexFor(math.NaN()); got != idxNaN {
		t.Fatalf("NaN: want %d, got %d", idxNaN, got)
	}
}
