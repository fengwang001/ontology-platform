package qr

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/hh"
)

const testEps = 1e-9

func TestReflector(t *testing.T) {
	cases := []struct {
		x    []float64
		ok   bool
		want []float64
	}{
		{[]float64{3, 4}, true, []float64{1, -2}},
		{[]float64{-3, 4}, true, []float64{1, 2}},
		{[]float64{0, 5}, true, []float64{1, -1}}, // sign(0)=+1
		{[]float64{5, 0, 0}, false, nil},
		{[]float64{1.0 / 5}, false, nil}, // NOTES k=1
	}
	for i, c := range cases {
		v, ok := hh.Reflector(c.x)
		if ok != c.ok {
			t.Fatalf("case %d: ok=%v want %v", i, ok, c.ok)
		}
		for j := range c.want {
			if math.Abs(v[j]-c.want[j]) > testEps {
				t.Fatalf("case %d: v=%v want %v", i, v, c.want)
			}
		}
	}
}

func TestApplyPreservesEarlierColumns(t *testing.T) {
	for _, n := range []int{2, 3, 5} {
		rng := rand.New(rand.NewSource(int64(n)))
		a := make([]float64, n*n)
		for i := range a {
			a[i] = rng.NormFloat64()*4 + 1
		}
		for k := 0; k < n; k++ {
			snap := append([]float64(nil), a...)
			x := make([]float64, n-k)
			for i := range x {
				x[i] = a[(k+i)*n+k]
			}
			v, ok := hh.Reflector(x)
			if !ok {
				continue
			}
			hh.Apply(v, a, n, k)
			for idx := range a { // 不变量3: 列 0..k-1 逐字节不变（消零另由重建测试覆盖）
				if idx%n < k && math.Float64bits(a[idx]) != math.Float64bits(snap[idx]) {
					t.Fatalf("n=%d k=%d: earlier col changed at %d", n, k, idx)
				}
			}
		}
	}
}

func TestFactorReconstruct(t *testing.T) {
	mats := [][]float64{{-7}, {3, 1, 4, 1}, {1, 2, 3, 0, 4, 5, 0, -6, 7}}
	for _, sz := range []int{4, 8, 16} { // 多档规模随机矩阵
		rng := rand.New(rand.NewSource(int64(sz * 100)))
		m := make([]float64, sz*sz)
		for i := range m {
			m[i] = rng.NormFloat64()*6 - 3
		}
		mats = append(mats, m)
	}
	for _, a0 := range mats {
		n := int(math.Sqrt(float64(len(a0))))
		src := append([]float64(nil), a0...)
		Q, R, err := Factor(a0, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for idx := 0; idx < n*n; idx++ {
			r, j := idx/n, idx%n
			var got float64 // 不变量1: Q·R 逐元素还原 A（≤1e-9）
			for p := 0; p < n; p++ {
				got += Q[r*n+p] * R[p*n+j]
			}
			if math.Abs(got-src[idx]) > testEps || (r > j && R[idx] != 0) {
				t.Fatalf("n=%d [%d][%d]: QR=%v a=%v Rbelow=%v", n, r, j, got, src[idx], R[idx])
			}
		}
	}
}

func TestUpperTriangleZeroReflectors(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n)
		rng := rand.New(rand.NewSource(int64(n)))
		for idx := range a { // 只填上三角（i<=j），下三角保持精确零
			if r, c := idx/n, idx%n; r <= c {
				a[idx] = rng.NormFloat64()*5 - 1
			}
		}
		if _, _, err := Factor(a, n); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := reflectorCount.Load(); got != 0 { // O(1) 判定跳过，不随 n 增长
			t.Fatalf("n=%d: constructed %d reflectors, want 0", n, got)
		}
	}
}

func TestRejectionNoSideEffect(t *testing.T) {
	before := reflectorCount.Load()
	bad := []struct {
		a   []float64
		n   int
		err error
	}{
		{nil, 0, ErrEmpty},
		{[]float64{1, 2, 3}, 2, ErrDimMismatch},
		{make([]float64, 8), 3, ErrDimMismatch},
		{[]float64{0, 1, 0, 2}, 2, ErrZeroTail}, // 列0自首元起全零
	}
	for _, c := range bad {
		snap := append([]float64(nil), c.a...)
		Q, R, err := Factor(c.a, c.n)
		if !errors.Is(err, c.err) || Q != nil || R != nil {
			t.Fatalf("a=%v n=%d: err=%v want %v", c.a, c.n, err, c.err)
		}
		for i := range c.a { // 被拒调用不改输入
			if c.a[i] != snap[i] {
				t.Fatalf("rejected call mutated input at %d", i)
			}
		}
	}
	if reflectorCount.Load() != before { // 不变量4: 拒绝不碰计数器
		t.Fatalf("counter changed: %d -> %d", before, reflectorCount.Load())
	}
	full := []float64{1, 2, 3, 4, 5, 6, 7, 8, 10}
	if _, _, e2 := Factor(full, 3); e2 != nil || reflectorCount.Load() != 2 { // 末列尾长1恒跳过，故构造2个
		t.Fatalf("unusable after rejection: err=%v count=%d want 2", e2, reflectorCount.Load())
	}
	if _, _, err := Factor(nil, 0); !errors.Is(err, ErrEmpty) || reflectorCount.Load() != 2 {
		t.Fatalf("post-success rejection left a trace")
	}
}
