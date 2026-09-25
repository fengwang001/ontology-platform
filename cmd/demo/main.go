package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status + " " + name)
}

// bigRef 逐项 coeff[i]*x^i 用 big.Int 精确累加（先算 x^i 再求和）。
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

func main() {
	// 1. 第三节八行表
	type row struct {
		coeff []int64
		x     int64
		want  int64
	}
	rows := []row{
		{[]int64{1, 2, 3}, 2, 17},
		{[]int64{5, -2, 1}, 3, 8},
		{[]int64{0, 0, 1}, 10, 100},
		{[]int64{1, 1, 1, 1}, -2, -5},
		{[]int64{1, 0, 1}, 67108864, 4503599627370497},
		{nil, 5, 0},
		{[]int64{1}, 999, 1},
	}
	ok := true
	for _, r := range rows {
		v, err := api.New(r.coeff).Eval(r.x)
		if err != nil || v != r.want {
			ok = false
		}
	}
	_, err8 := api.New([]int64{0, 3037000500}).Eval(3037000500)
	check("八行表: 17 8 100 -5 4503599627370497 0 1 溢出", ok && errors.Is(err8, api.ErrMulOverflow))

	// 2. float64 丢 +1：2^53+1 被舍入回 2^53（2^52+1 仍可精确表示，见 NOTES 甲）
	f := float64(int64(1) << 53)
	check("float64 累加丢 +1: 2^53+1 -> 9007199254740992", f+1 == f)

	// 3. 3037000500^2 溢出；朴素 int64 静默回绕成负的错值
	base := int64(3037000500)
	wrapped := base * base // 运行时相乘：不检溢出则静默回绕
	exact, _ := new(big.Int).SetString("9223372037000250000", 10)
	check("3037000500^2 报乘法溢出; 回绕错值 -9223372036709301616",
		errors.Is(err8, api.ErrMulOverflow) && wrapped == -9223372036709301616 && exact.Cmp(big.NewInt(wrapped)) != 0)

	// 4. 系数大端读反的错值：17 错成 11
	rev, _ := api.New([]int64{3, 2, 1}).Eval(2)
	check("大端读反 [1,2,3]@2: 17 错成 11", rev == 11)

	// 5. Horner 与逐项 big 参照等价（多组小值，含负数 x）
	ok = true
	for _, r := range append(rows, row{[]int64{7, -3, 11, -13, 2}, 6, 0}, row{[]int64{-4, 5}, -9, 0}) {
		v, err := api.New(r.coeff).Eval(r.x)
		if err != nil || bigRef(r.coeff, r.x).Cmp(big.NewInt(v)) != 0 {
			ok = false
		}
	}
	check("Horner 与逐项 big 参照一致", ok)

	// 6. 空/全零/常数
	z1, _ := api.New(nil).Eval(5)
	z2, _ := api.New([]int64{0, 0, 0}).Eval(-7)
	c1, _ := api.New([]int64{-9}).Eval(0)
	c2, _ := api.New([]int64{-9}).Eval(12345)
	check("空=0 全零=0 常数与 x 无关", z1 == 0 && z2 == 0 && c1 == -9 && c2 == -9)

	// 7. 三类可判定错误互不相同
	_, eMul := api.New([]int64{0, 3037000500}).Eval(3037000500)
	_, eAdd := api.New([]int64{math.MaxInt64, 1}).Eval(1)
	_, eDeg := api.New(make([]int64, (1<<20)+1)).Eval(1)
	distinct := !errors.Is(api.ErrMulOverflow, api.ErrAddOverflow) &&
		!errors.Is(api.ErrMulOverflow, api.ErrDegreeExceeded) &&
		!errors.Is(api.ErrAddOverflow, api.ErrDegreeExceeded)
	check("乘/加溢出与次数超限三类错误可判定且互异",
		errors.Is(eMul, api.ErrMulOverflow) && errors.Is(eAdd, api.ErrAddOverflow) &&
			errors.Is(eDeg, api.ErrDegreeExceeded) && distinct)

	// 8. 被拒后无副作用：返回 0，同一 Poly 仍可正常求值
	bad := api.New([]int64{0, 3037000500})
	v0, _ := bad.Eval(3037000500)
	v1, err1 := bad.Eval(2)
	check("被拒后返回 0 且 Poly 可继续用", v0 == 0 && err1 == nil && v1 == 6074001000)

	// 9. 乘加次数恰为次数 m（O(m)）：由 eval 包内测试钉死；SelfCheck 核验四不变量
	check("乘加次数=m 见包内测试; SelfCheck 通过", api.New(nil).SelfCheck() == nil)

	// 10. 并发只读同一 *Poly，同一批 x 结果逐条相同
	shared := api.New([]int64{7, -3, 11, -13, 2, 5})
	xs := []int64{-3, -1, 0, 1, 2, 7}
	want := make([]int64, len(xs))
	for i, x := range xs {
		want[i], _ = shared.Eval(x)
	}
	var wg sync.WaitGroup
	results := make([][]int64, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rs := make([]int64, len(xs))
			for i, x := range xs {
				rs[i], _ = shared.Eval(x)
			}
			results[g] = rs
		}(g)
	}
	wg.Wait()
	ok = true
	for _, rs := range results {
		for i := range xs {
			if rs[i] != want[i] {
				ok = false
			}
		}
	}
	check("16 goroutine 并发只读结果逐条一致", ok)

	if failed {
		os.Exit(1)
	}
}
