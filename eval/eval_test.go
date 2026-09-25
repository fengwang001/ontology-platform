package eval

import (
	"errors"
	"math"
	"testing"
)

// TestOpCountLinear 钉住乘加次数：次数 m 时恰好 m 次，证明 Horner 是 O(m)。
func TestOpCountLinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		coeff := make([]int64, m+1)
		for i := range coeff {
			coeff[i] = 1
		}
		if _, err := Eval(coeff, 0); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got := lastOps.Load(); got != int64(m) {
			t.Errorf("m=%d: ops=%d, want %d", m, got, m)
		}
	}
}

func TestMul(t *testing.T) {
	cases := []struct {
		a, b, want int64
		err        bool
	}{
		{0, 5, 0, false},
		{3, -4, -12, false},
		{-3, -4, 12, false},
		{math.MaxInt64, 1, math.MaxInt64, false},
		{math.MinInt64, 1, math.MinInt64, false},
		{3037000499, 3037000499, 9223372030926249001, false},
		{math.MaxInt64, 2, 0, true},
		{math.MinInt64, -1, 0, true},
		{math.MinInt64, 2, 0, true},
		{3037000500, 3037000500, 0, true},
		{-3037000500, 3037000500, 0, true},
	}
	for _, c := range cases {
		got, err := mul(c.a, c.b)
		if c.err {
			if !errors.Is(err, ErrMulOverflow) {
				t.Errorf("mul(%d,%d): err=%v, want ErrMulOverflow", c.a, c.b, err)
			}
		} else if err != nil || got != c.want {
			t.Errorf("mul(%d,%d)=(%d,%v), want (%d,nil)", c.a, c.b, got, err, c.want)
		}
	}
}

func TestAdd(t *testing.T) {
	cases := []struct {
		a, b, want int64
		err        bool
	}{
		{0, 0, 0, false},
		{-5, 3, -2, false},
		{math.MaxInt64, 0, math.MaxInt64, false},
		{math.MinInt64, 0, math.MinInt64, false},
		{math.MaxInt64, 1, 0, true},
		{math.MinInt64, -1, 0, true},
		{math.MaxInt64, math.MaxInt64, 0, true},
		{math.MinInt64, math.MinInt64, 0, true},
	}
	for _, c := range cases {
		got, err := add(c.a, c.b)
		if c.err {
			if !errors.Is(err, ErrAddOverflow) {
				t.Errorf("add(%d,%d): err=%v, want ErrAddOverflow", c.a, c.b, err)
			}
		} else if err != nil || got != c.want {
			t.Errorf("add(%d,%d)=(%d,%v), want (%d,nil)", c.a, c.b, got, err, c.want)
		}
	}
}

// TestEvalErrorKinds 三类故障注入报互不相同的可判定错误，且不返回半成品。
func TestEvalErrorKinds(t *testing.T) {
	cases := []struct {
		name  string
		coeff []int64
		x     int64
		err   error
	}{
		{"mul", []int64{0, 3037000500}, 3037000500, ErrMulOverflow},
		{"add", []int64{1, 1}, math.MaxInt64, ErrAddOverflow},
		{"degree", make([]int64, (1<<20)+1), 1, ErrDegreeLimit},
	}
	for _, c := range cases {
		v, err := Eval(c.coeff, c.x)
		if !errors.Is(err, c.err) || v != 0 {
			t.Errorf("%s: Eval=(%d,%v), want (0,%v)", c.name, v, err, c.err)
		}
	}
	if ErrMulOverflow == ErrAddOverflow || ErrAddOverflow == ErrDegreeLimit ||
		ErrMulOverflow == ErrDegreeLimit {
		t.Error("sentinel errors must be distinct")
	}
}
