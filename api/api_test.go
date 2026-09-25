package api_test

import (
	"errors"
	"math"
	"math/big"
	"sync"
	"testing"

	"ontology/api"
	"ontology/eval"
)

var refCases = []struct {
	coeff []int64
	x     int64
}{
	{[]int64{1, 2, 3}, 2}, {[]int64{5, -2, 1}, 3}, {[]int64{0, 0, 1}, 10},
	{[]int64{1, 1, 1, 1}, -2}, {[]int64{1, 0, 1}, 67108864},
	{[]int64{7, -3, 9, -4, 2}, 5}, {[]int64{-8, 6}, -7},
	{[]int64{0, 0, 0, 9}, 4}, {[]int64{-1, -1, -1}, -3},
}

// bigRef 用 math/big 逐项 coeff[i]*x^i 精确累加，作为参照。
func bigRef(coeff []int64, x int64) int64 {
	total, bx := new(big.Int), big.NewInt(x)
	for i, c := range coeff {
		t := new(big.Int).Exp(bx, big.NewInt(int64(i)), nil)
		total.Add(total, t.Mul(t, big.NewInt(c)))
	}
	return total.Int64()
}

// TestMatchesBigReference 不变量1：无溢出时 Eval 与 big 精确参照一致。
func TestMatchesBigReference(t *testing.T) {
	for _, c := range refCases {
		got, err := api.New(c.coeff).Eval(c.x)
		if want := bigRef(c.coeff, c.x); err != nil || got != want {
			t.Errorf("Eval(%v,%d)=(%d,%v), want %d", c.coeff, c.x, got, err, want)
		}
	}
}

// TestHornerEqualsTermwise 不变量2：Horner 与「先算 x^i 再逐项求和」一致。
func TestHornerEqualsTermwise(t *testing.T) {
	for _, c := range refCases {
		sum, pow := int64(0), int64(1)
		for _, co := range c.coeff {
			sum, pow = sum+co*pow, pow*c.x
		}
		got, err := api.New(c.coeff).Eval(c.x)
		if err != nil || got != sum {
			t.Errorf("Eval(%v,%d)=(%d,%v), want %d", c.coeff, c.x, got, err, sum)
		}
	}
}

// TestEmptyAndConstant 不变量3：空/全零多项式为 0，常数多项式与 x 无关。
func TestEmptyAndConstant(t *testing.T) {
	cases := []struct {
		coeff []int64
		want  int64
	}{
		{nil, 0}, {[]int64{}, 0}, {[]int64{0, 0, 0}, 0},
		{[]int64{42}, 42}, {[]int64{-7}, -7},
	}
	for _, c := range cases {
		for _, x := range []int64{0, 1, -7, 999, math.MinInt64} {
			got, err := api.New(c.coeff).Eval(x)
			if err != nil || got != c.want {
				t.Errorf("Eval(%v,%d)=(%d,%v), want %d", c.coeff, x, got, err, c.want)
			}
		}
	}
	if d := api.New(nil).Degree(); d != -1 {
		t.Errorf("Degree(empty)=%d, want -1", d)
	}
	if d := api.New([]int64{1, 2, 3}).Degree(); d != 2 {
		t.Errorf("Degree=%d, want 2", d)
	}
}

// TestDistinctErrorsNoPartial 不变量4：三类错误互不相同、可判定，被拒后无副作用。
func TestDistinctErrorsNoPartial(t *testing.T) {
	cases := []struct {
		coeff []int64
		x     int64
		err   error
	}{
		{[]int64{0, 3037000500}, 3037000500, eval.ErrMulOverflow},
		{[]int64{1, 1}, math.MaxInt64, eval.ErrAddOverflow},
		{make([]int64, (1<<20)+1), 1, eval.ErrDegreeLimit},
	}
	for _, c := range cases {
		if v, err := api.New(c.coeff).Eval(c.x); !errors.Is(err, c.err) || v != 0 {
			t.Errorf("Eval=(%d,%v), want (0,%v)", v, err, c.err)
		}
	}
	if eval.ErrMulOverflow == eval.ErrAddOverflow || eval.ErrAddOverflow == eval.ErrDegreeLimit ||
		eval.ErrMulOverflow == eval.ErrDegreeLimit {
		t.Error("sentinel errors must be distinct")
	}
	p := api.New([]int64{0, 3037000500}) // 被拒后不留痕
	if _, err := p.Eval(3037000500); err == nil {
		t.Fatal("want overflow")
	}
	if v, err := p.Eval(1); err != nil || v != 3037000500 {
		t.Errorf("after rejection: (%d,%v), want (3037000500,nil)", v, err)
	}
}

// TestConcurrentReadOnly 并发只读：N 个 goroutine 对同一 Poly 求值，结果逐条相同。
func TestConcurrentReadOnly(t *testing.T) {
	p := api.New([]int64{1, 2, 3, 4, 5})
	xs := []int64{0, 1, -1, 2, -3, 7, 100}
	want := make([]int64, len(xs))
	for i, x := range xs {
		want[i], _ = p.Eval(x)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i, x := range xs {
				if v, err := p.Eval(x); err != nil || v != want[i] {
					t.Errorf("mismatch x=%d got=%d want=%d", x, v, want[i])
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	if !api.New([]int64{1, 2, 3}).SelfCheck() {
		t.Error("SelfCheck should pass")
	}
}
