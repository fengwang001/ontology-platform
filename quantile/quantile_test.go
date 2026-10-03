package quantile_test

import (
	"errors"
	"math/big"
	"math/rand/v2"
	"slices"
	"testing"

	"ontology/merge"
	"ontology/quantile"
	"ontology/schema"
)

func hist(bounds, counts []int64, sum int64) schema.Hist {
	return schema.Hist{Name: "q", Bounds: bounds, Counts: counts, Sum: sum}
}

// 规格书示例：合并结果 [20,100] / [6,13,3]，N=22。
func TestQuantileExample(t *testing.T) {
	a := hist([]int64{10, 20, 50, 100}, []int64{3, 2, 4, 1, 0}, 500)
	b := hist([]int64{20, 100}, []int64{1, 8, 3}, 700)
	m, err := merge.Merge(&a, &b)
	if err != nil {
		t.Fatal(err)
	}
	v, sat, err := quantile.Quantile(&m, 500)
	if err != nil || sat || v != 50 { // r=11，桶 1，20+⌊80×5/13⌋=50
		t.Fatalf("q=500: (%d, %v, %v)", v, sat, err)
	}
	v, sat, err = quantile.Quantile(&m, 950)
	if err != nil || !sat || v != 100 { // r=21，溢出桶，返回最后边界并标记
		t.Fatalf("q=950: (%d, %v, %v)", v, sat, err)
	}
	v, sat, err = quantile.Quantile(&m, 0)
	if err != nil || sat || v != 3 { // r=1，桶 0，⌊20×1/6⌋=3
		t.Fatalf("q=0: (%d, %v, %v)", v, sat, err)
	}
	t.Logf("输入 %v -> q500=%d q950=(%d,saturated) q0=%d（依据：秩 r=max(1,⌈qN/1000⌉)，桶内 lo+⌊(hi-lo)w/c⌋）",
		m, 50, 100, 3)
}

func TestRankAndInterpolationRounding(t *testing.T) {
	h := hist([]int64{10, 20}, []int64{4, 4, 0}, 100) // N=8
	cases := []struct {
		q    int64
		want int64
	}{
		{0, 2},     // r=max(1,0)=1，⌊10×1/4⌋=2
		{125, 2},   // r=⌈1.0⌉=1（恰整不向上）
		{126, 5},   // r=⌈1.008⌉=2，⌊10×2/4⌋=5
		{250, 5},   // r=2
		{251, 7},   // r=⌈2.008⌉=3，⌊10×3/4⌋=7（向下取整）
		{1000, 20}, // r=8，桶 1，lo=10，10+⌊10×4/4⌋=20
	}
	for _, tc := range cases {
		v, sat, err := quantile.Quantile(&h, tc.q)
		if err != nil || sat {
			t.Fatalf("q=%d: (%d,%v,%v)", tc.q, v, sat, err)
		}
		if v != tc.want {
			t.Fatalf("q=%d: got %d want %d", tc.q, v, tc.want)
		}
	}
}

func TestQZeroAndQThousand(t *testing.T) {
	h := hist([]int64{7}, []int64{3, 0}, 10)         // N=3
	if v, _, _ := quantile.Quantile(&h, 0); v != 2 { // r=1，⌊7×1/3⌋=2
		t.Fatalf("q=0: %d", v)
	}
	if v, _, _ := quantile.Quantile(&h, 1000); v != 7 { // r=3，⌊7×3/3⌋=7
		t.Fatalf("q=1000: %d", v)
	}
	if v, _, _ := quantile.Quantile(&h, 334); v != 4 { // r=⌈1.002⌉=2，⌊14/3⌋=4
		t.Fatalf("q=334: %d", v)
	}
}

// 128 位乘积：(hi-lo)×w = 10^12 × 10^10 = 10^22 > 2^64，64 位会溢出。
func TestWideProduct128(t *testing.T) {
	c := int64(10_000_000_000)
	h := hist([]int64{1_000_000_000_000}, []int64{c, 0}, 0)
	v, sat, err := quantile.Quantile(&h, 1000) // w=c，值=⌊1e12×1e10/1e10⌋=1e12
	if err != nil || sat || v != 1_000_000_000_000 {
		t.Fatalf("q=1000: (%d,%v,%v)", v, sat, err)
	}
	v, _, _ = quantile.Quantile(&h, 500) // w=5e9，值=5e11
	if v != 500_000_000_000 {
		t.Fatalf("q=500: %d", v)
	}
	t.Logf("128 位乘积判定：1e12×1e10=1e22 超出 uint64，结果 %d 精确", 1_000_000_000_000)
}

func TestQuantileErrors(t *testing.T) {
	h := hist([]int64{10}, []int64{1, 0}, 5)
	for _, q := range []int64{-1, 1001} {
		if _, _, err := quantile.Quantile(&h, q); !errors.Is(err, schema.ErrInvalid) {
			t.Fatalf("q=%d: %v", q, err)
		}
	}
	bad := hist([]int64{10}, []int64{1}, 5)
	if _, _, err := quantile.Quantile(&bad, 500); !errors.Is(err, schema.ErrInvalid) {
		t.Fatalf("非法 h: %v", err)
	}
	empty := hist([]int64{10}, []int64{0, 0}, 0)
	if _, _, err := quantile.Quantile(&empty, 500); !errors.Is(err, schema.ErrEmpty) {
		t.Fatalf("空: %v", err)
	}
	if _, _, err := quantile.Quantile(&empty, -1); !errors.Is(err, schema.ErrInvalid) { // 参数非法优先于空
		t.Fatalf("顺序: %v", err)
	}
}

// naiveQuantile 用 math/big 按规格逐条写成的朴素模拟。
func naiveQuantile(h schema.Hist, q int64) (int64, bool) {
	n := new(big.Int)
	for _, c := range h.Counts {
		n.Add(n, big.NewInt(c))
	}
	r := new(big.Int).Mul(big.NewInt(q), n)
	r.Add(r, big.NewInt(999))
	r.Div(r, big.NewInt(1000))
	if r.Sign() == 0 {
		r.SetInt64(1)
	}
	cum := new(big.Int)
	for i, c := range h.Counts {
		if new(big.Int).Add(cum, big.NewInt(c)).Cmp(r) >= 0 {
			if i == len(h.Bounds) {
				return h.Bounds[len(h.Bounds)-1], true
			}
			lo := int64(0)
			if i > 0 {
				lo = h.Bounds[i-1]
			}
			w := new(big.Int).Sub(r, cum)
			num := new(big.Int).Mul(big.NewInt(h.Bounds[i]-lo), w)
			num.Div(num, big.NewInt(c))
			return lo + num.Int64(), false
		}
		cum.Add(cum, big.NewInt(c))
	}
	panic("unreachable")
}

func TestQuantileAgainstNaiveAndMonotonic(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9))
	for trial := 0; trial < 300; trial++ {
		nb := 1 + r.Int64N(6)
		var bounds []int64
		for int64(len(bounds)) < nb {
			b := 1 + r.Int64N(1_000_000_000_000)
			if !slices.Contains(bounds, b) {
				bounds = append(bounds, b)
			}
		}
		slices.Sort(bounds)
		counts := make([]int64, nb+1)
		for i := range counts {
			counts[i] = r.Int64N(1000)
		}
		h := hist(bounds, counts, 0)
		var prev int64
		for q := int64(0); q <= 1000; q += 50 {
			v, sat, err := quantile.Quantile(&h, q)
			var total int64
			for _, c := range counts {
				total += c
			}
			if total == 0 {
				if !errors.Is(err, schema.ErrEmpty) {
					t.Fatalf("trial %d q=%d: %v", trial, q, err)
				}
				break
			}
			if err != nil {
				t.Fatalf("trial %d q=%d: %v", trial, q, err)
			}
			wv, wsat := naiveQuantile(h, q)
			if v != wv || sat != wsat {
				t.Fatalf("trial %d q=%d: got (%d,%v) want (%d,%v)\nh=%v", trial, q, v, sat, wv, wsat, h)
			}
			if q > 0 && v < prev {
				t.Fatalf("trial %d: q 单调性破坏 q=%d v=%d prev=%d", trial, q, v, prev)
			}
			prev = v
			if trial < 2 && q%500 == 0 {
				t.Logf("trial %d 输入 h=%v q=%d 输出 (%d,saturated=%v)（判定：与 big.Int 朴素模拟一致）",
					trial, h, q, v, sat)
			}
		}
	}
}
