package eval

import (
	"errors"
	"math"
	"testing"

	"ontology/poly"
)

// 乘加次数恰为次数 m（O(m) 而非逐项先算 x^i 的 O(m^2)）。
// 计数器是非导出字段，只能在本包内测试里观测。
func TestOpsCountEqualsDegree(t *testing.T) {
	for _, m := range []int64{100, 300, 1000, 3000, 10000} {
		coeff := make([]int64, m+1)
		for i := range coeff {
			coeff[i] = 1
		}
		if _, err := Eval(poly.New(coeff), 0); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got := lastOps.Load(); got != m {
			t.Fatalf("m=%d: ops=%d, want exactly %d", m, got, m)
		}
	}
}

func TestMul(t *testing.T) {
	cases := []struct {
		a, b, want int64
		overflow   bool
	}{
		{0, 0, 0, false},
		{3, -2, -6, false},
		{-3, -2, 6, false},
		{-3, 2, -6, false},
		{1 << 31, 1 << 31, 1 << 62, false},
		{math.MinInt64, 1, math.MinInt64, false},
		{math.MaxInt64, 1, math.MaxInt64, false},
		{math.MinInt64, -1, 0, true},
		{math.MaxInt64, 2, 0, true},
		{math.MinInt64, 2, 0, true},
		{3037000500, 3037000500, 0, true},
		{-3037000500, 3037000500, 0, true},
	}
	for _, c := range cases {
		got, err := mul(c.a, c.b)
		if c.overflow {
			if !errors.Is(err, ErrMulOverflow) || got != 0 {
				t.Fatalf("mul(%d,%d): got %d,%v; want 0,ErrMulOverflow", c.a, c.b, got, err)
			}
		} else if err != nil || got != c.want {
			t.Fatalf("mul(%d,%d): got %d,%v; want %d,nil", c.a, c.b, got, err, c.want)
		}
	}
}

func TestAdd(t *testing.T) {
	cases := []struct {
		a, b, want int64
		overflow   bool
	}{
		{1, 2, 3, false},
		{-1, -2, -3, false},
		{math.MaxInt64, -1, math.MaxInt64 - 1, false},
		{math.MinInt64, 1, math.MinInt64 + 1, false},
		{math.MaxInt64, 1, 0, true},
		{math.MinInt64, -1, 0, true},
		{math.MaxInt64, math.MaxInt64, 0, true},
		{math.MinInt64, math.MinInt64, 0, true},
	}
	for _, c := range cases {
		got, err := add(c.a, c.b)
		if c.overflow {
			if !errors.Is(err, ErrAddOverflow) || got != 0 {
				t.Fatalf("add(%d,%d): got %d,%v; want 0,ErrAddOverflow", c.a, c.b, got, err)
			}
		} else if err != nil || got != c.want {
			t.Fatalf("add(%d,%d): got %d,%v; want %d,nil", c.a, c.b, got, err, c.want)
		}
	}
}

// 经 Eval 触发的三类故障：错误可判定、互不相同、返回 0 不留半成品。
func TestEvalFaultsDistinct(t *testing.T) {
	cases := []struct {
		name  string
		coeff []int64
		x     int64
		want  error
	}{
		{"mul overflow", []int64{0, 3037000500}, 3037000500, ErrMulOverflow},
		{"add overflow", []int64{math.MaxInt64, 1}, 1, ErrAddOverflow},
		{"degree exceeded", make([]int64, (1<<20)+1), 1, poly.ErrDegreeExceeded},
	}
	for _, c := range cases {
		got, err := Eval(poly.New(c.coeff), c.x)
		if !errors.Is(err, c.want) || got != 0 {
			t.Fatalf("%s: got %d,%v; want 0,%v", c.name, got, err, c.want)
		}
	}
	if errors.Is(ErrMulOverflow, ErrAddOverflow) || errors.Is(ErrAddOverflow, ErrMulOverflow) {
		t.Fatal("mul/add overflow errors must be distinct")
	}
}
