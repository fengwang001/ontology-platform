package api_test

import (
	"errors"
	"math"
	"math/big"
	"sync"
	"testing"

	"ontology/api"
)

// bigRef 朴素参照：逐项 coeff[i]*x^i 用 big.Int 精确累加。
func bigRef(coeff []int64, x int64) *big.Int {
	total := new(big.Int)
	bx := big.NewInt(x)
	pow := big.NewInt(1)
	for _, c := range coeff {
		total.Add(total, new(big.Int).Mul(big.NewInt(c), pow))
		pow.Mul(pow, bx)
	}
	return total
}

var smallCases = []struct {
	coeff []int64
	x     int64
}{
	{[]int64{1, 2, 3}, 2},
	{[]int64{5, -2, 1}, 3},
	{[]int64{0, 0, 1}, 10},
	{[]int64{1, 1, 1, 1}, -2},
	{[]int64{1, 0, 1}, 67108864},
	{[]int64{7, -3, 11, -13, 2}, 6},
	{[]int64{-4, 5}, -9},
	{[]int64{9, -8, 7, -6, 5, -4, 3, -2, 1}, -5},
	{[]int64{-42}, 12345},
}

// 不变量 1：与 big 精确参照一致。
func TestEvalVsBigRef(t *testing.T) {
	for _, c := range smallCases {
		got, err := api.New(c.coeff).Eval(c.x)
		want := bigRef(c.coeff, c.x)
		if err != nil || !want.IsInt64() || want.Int64() != got {
			t.Fatalf("Eval(%v,%d)=%d,%v; big ref=%v", c.coeff, c.x, got, err, want)
		}
	}
}

// 不变量 2：Horner 与「先算 x^i 再逐项求和」（int64，小值不溢出）等价。
func TestHornerEquivTermwise(t *testing.T) {
	for _, c := range smallCases {
		sum, pow := int64(0), int64(1)
		for _, k := range c.coeff {
			sum += k * pow
			pow *= c.x
		}
		got, err := api.New(c.coeff).Eval(c.x)
		if err != nil || got != sum {
			t.Fatalf("Eval(%v,%d)=%d,%v; termwise=%d", c.coeff, c.x, got, err, sum)
		}
	}
}

// 不变量 3：空/全零多项式为 0，常数多项式与 x 无关。
func TestEmptyZeroConstant(t *testing.T) {
	cases := []struct {
		coeff []int64
		x     int64
		want  int64
	}{
		{nil, 5, 0},
		{[]int64{0, 0, 0}, 7, 0},
		{[]int64{1}, 999, 1},
		{[]int64{-9}, 0, -9},
		{[]int64{-9}, math.MinInt64, -9},
	}
	for _, c := range cases {
		got, err := api.New(c.coeff).Eval(c.x)
		if err != nil || got != c.want {
			t.Fatalf("Eval(%v,%d)=%d,%v; want %d,nil", c.coeff, c.x, got, err, c.want)
		}
	}
}

// 不变量 4：三类故障报互不相同的可判定错误，返回 0 不留半成品、不 panic。
func TestErrorsDistinctNoPartial(t *testing.T) {
	cases := []struct {
		coeff []int64
		x     int64
		want  error
	}{
		{[]int64{0, 3037000500}, 3037000500, api.ErrMulOverflow},
		{[]int64{math.MaxInt64, 1}, 1, api.ErrAddOverflow},
		{make([]int64, (1<<20)+1), 1, api.ErrDegreeExceeded},
	}
	for _, c := range cases {
		got, err := api.New(c.coeff).Eval(c.x)
		if !errors.Is(err, c.want) || got != 0 {
			t.Fatalf("Eval: got %d,%v; want 0,%v", got, err, c.want)
		}
	}
	if errors.Is(api.ErrMulOverflow, api.ErrAddOverflow) ||
		errors.Is(api.ErrMulOverflow, api.ErrDegreeExceeded) ||
		errors.Is(api.ErrAddOverflow, api.ErrDegreeExceeded) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

// 并发：32 个 goroutine 只读同一个 *Poly，同一批 x 的结果逐条相同。
func TestConcurrentReadOnly(t *testing.T) {
	p := api.New([]int64{7, -3, 11, -13, 2, 5})
	xs := []int64{-3, -1, 0, 1, 2, 7}
	want := make([]int64, len(xs))
	for i, x := range xs {
		var err error
		if want[i], err = p.Eval(x); err != nil {
			t.Fatal(err)
		}
	}
	const n = 32
	results := make([][]int64, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rs := make([]int64, len(xs))
			for i, x := range xs {
				rs[i], _ = p.Eval(x)
			}
			results[g] = rs
		}(g)
	}
	wg.Wait()
	for g, rs := range results {
		for i := range xs {
			if rs[i] != want[i] {
				t.Fatalf("goroutine %d x=%d: %d != %d", g, xs[i], rs[i], want[i])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New([]int64{1, 2, 3}).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
