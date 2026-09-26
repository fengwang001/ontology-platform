package api_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/api"
)

func randMat(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	a := make([]float64, n*n)
	for i := range a {
		a[i] = rng.Float64() - 0.5
		if i/n == i%n {
			a[i] += float64(n) // 对角占优，保证良态
		}
	}
	return a
}

func maxErrI(a, inv []float64, n int) (m float64) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += a[i*n+k] * inv[k*n+j]
			}
			if i == j {
				sum--
			}
			if d := math.Abs(sum); d > m {
				m = d
			}
		}
	}
	return m
}

// 不变量 1：任意可逆矩阵 A·A⁻¹ == I（每项误差 ≤ 1e-9）。
func TestInvIdentity(t *testing.T) {
	h := api.New()
	cases := []struct {
		name string
		n    int
		a    []float64
	}{
		{"one", 1, []float64{-4}},
		{"spec-2x2", 2, []float64{0, 2, 1, 1}},
		{"neg-zeros-3x3", 3, []float64{2, 0, 1, -1, 3, 0, 0, -2, 4}},
		{"rand-64", 64, randMat(64, 3)},
	}
	for _, tc := range cases {
		inv, err := h.Inv(tc.a, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		if got := maxErrI(tc.a, inv, tc.n); got > 1e-9 {
			t.Fatalf("%s: max |A*inv-I| = %v > 1e-9", tc.name, got)
		}
	}
}

// 不变量 2：可精确表示的逆必须逐位精确。
func TestExactInverse(t *testing.T) {
	inv, err := api.New().Inv([]float64{0, 2, 1, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{-0.5, 1, 0.5, 0}; !slices.Equal(inv, want) {
		t.Fatalf("inv = %v, want exact %v", inv, want)
	}
}

// 不变量 3：输入逐字节不变。
func TestInputUnmodified(t *testing.T) {
	h := api.New()
	for _, tc := range []struct {
		n int
		a []float64
	}{
		{2, []float64{0, 2, 1, 1}},
		{4, []float64{0, -1, 2, 0, 3, 0, 0, 1, 0, 2, -1, 0, 1, 0, 0, -2}},
		{23, randMat(23, 7)},
	} {
		before := slices.Clone(tc.a)
		if _, err := h.Inv(tc.a, tc.n); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(tc.a, before) {
			t.Fatalf("n=%d: input modified", tc.n)
		}
	}
}

// 三类故障注入：哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	h := api.New()
	cases := []struct {
		name string
		a    []float64
		n    int
		want error
	}{
		{"dim-mismatch", make([]float64, 3), 2, api.ErrDim},
		{"empty-nil", nil, 0, api.ErrEmpty},
		{"singular-rows", []float64{1, 2, 2, 4}, 2, api.ErrSingular},
	}
	all := []error{api.ErrDim, api.ErrEmpty, api.ErrSingular}
	for _, tc := range cases {
		_, err := h.Inv(tc.a, tc.n)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		for _, other := range all {
			if other != tc.want && errors.Is(err, other) {
				t.Fatalf("%s: err %v also matches %v", tc.name, err, other)
			}
		}
	}
}

// 不变量 4：被拒后状态不变，仍可正常使用且结果一致。
func TestFailureNoTrace(t *testing.T) {
	h := api.New()
	good := []float64{0, 2, 1, 1}
	want, err := h.Inv(good, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i, bad := range [][]float64{make([]float64, 3), nil, {1, 2, 2, 4}} {
		if _, err := h.Inv(bad, []int{2, 0, 2}[i]); err == nil {
			t.Fatal("expected rejection")
		}
	}
	got, err := h.Inv(good, 2)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("state changed after rejections: %v, %v", got, err)
	}
}

// 自检方法必须覆盖四条不变量并通过。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
