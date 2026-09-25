package main

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/num"
)

var failed bool

func report(name, detail string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s (%s)\n", name, status, detail)
}

// signedGcd 演示错误实现：带符号参数直接做欧几里得，gcd(6,-8) 返回负数。
func signedGcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// fibPair 返回相邻斐波那契项 (F_k, F_{k-1})，其中 F_k 至少有 digits 位十进制位。
func fibPair(digits int) (*big.Int, *big.Int) {
	a, b := big.NewInt(1), big.NewInt(1)
	threshold := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits-1)), nil)
	for b.Cmp(threshold) < 0 {
		a, b = b, new(big.Int).Add(a, b) // 新分配，避免接收方别名污染序列
	}
	return b, a
}

func must(n, d int64) *api.Rat {
	r, err := api.New(n, d)
	if err != nil {
		panic(err)
	}
	return r
}

func main() {
	// 1. 第三节七步运算，逐步的规范 n/d。
	v, err := api.New(6, -8)
	seq := []struct {
		f    func(x, y *api.Rat) (*api.Rat, error)
		n, d int64
	}{
		{api.Add, 5, 6}, {api.Mul, -4, 1}, {api.Sub, 1, 6},
		{api.Div, -3, 1}, {api.Add, 1, 6}, {api.Mul, 0, 5},
	}
	want := []string{"-3/4", "1/12", "-1/3", "-1/2", "1/6", "1/3", "0/1"}
	got := []string{v.String()}
	ok := err == nil
	for _, s := range seq {
		if v, err = s.f(v, must(s.n, s.d)); err != nil {
			ok = false
			break
		}
		got = append(got, v.String())
	}
	for i := range want {
		ok = ok && i < len(got) && got[i] == want[i]
	}
	report("steps", strings.Join(got, " "), ok && len(got) == 7)

	// 2. num：gcd 带符号参数 vs 对绝对值求 gcd 的差异。
	sg, ag := signedGcd(6, -8), num.Gcd(6, -8)
	report("signed-gcd", fmt.Sprintf("signed=%d abs=%d; 错值 3/-4, 正确 -3/4", sg, ag),
		sg == -2 && ag == 2)

	// 3. 交叉相乘不约分的非既约形式 vs 正确规范形式。
	rawN, rawD := 3*6+5*4, 4*6 // 38/24，且把 -3/4 符号外提，值也错
	correct, _ := api.Add(must(-3, 4), must(5, 6))
	report("crossmul", fmt.Sprintf("未约分 %d/%d, 正确 %s", rawN, rawD, correct),
		rawN == 38 && rawD == 24 && correct.String() == "1/12")

	// 4. 2^62+2^62：朴素 int64 静默回绕成负值，正确实现报 ErrAddOverflow。
	x := int64(1) << 62
	naive := x + x // 静默回绕
	_, err = api.Add(must(x, 1), must(x, 1))
	report("overflow-add", fmt.Sprintf("朴素回绕成 %d, 正确报 ErrAddOverflow", naive),
		naive == -9223372036854775808 && errors.Is(err, api.ErrAddOverflow))

	// 5. 四则与朴素参照一致（SelfCheck 内置序列核验四条不变量）。
	report("selfcheck", "四条不变量（参照一致/规范唯一/交换律/失败不留痕）", api.SelfCheck() == nil)

	// 6. 三类可判定的哨兵错误，互不相同。
	_, e1 := api.New(1, 0)
	_, e2 := api.Mul(must(1<<62, 1), must(1<<62, 1))
	_, e3 := api.Add(must(1<<62, 1), must(1<<62, 1))
	report("errors", "ErrZeroDenominator / ErrMulOverflow / ErrAddOverflow",
		errors.Is(e1, api.ErrZeroDenominator) && errors.Is(e2, api.ErrMulOverflow) &&
			errors.Is(e3, api.ErrAddOverflow) && e1 != e2 && e2 != e3 && e1 != e3)

	// 7. 被拒后状态不变，且仍可正常使用。
	r := must(3, 4)
	before := r.String()
	_, _ = api.New(1, 0)
	_, _ = api.Mul(must(1<<62, 1), must(1<<62, 1))
	w, err := api.Add(r, r)
	report("no-mutate", "拒绝后 r 不变且 Add(r,r)="+w.String(),
		r.String() == before && err == nil && w.String() == "3/2")

	// 8. num：gcd 迭代步数随位数线性（欧几里得）。10000 位的斐波那契最坏情形
	// 若用暴力试除需 ~10^10000 次，此处瞬间返回，行为上证明是欧几里得。
	f1, f2 := fibPair(10000)
	report("gcd-linear", "m=10000 位斐波那契相邻项 gcd=1, 欧几里得瞬间完成",
		num.GcdBig(f1, f2).Cmp(big.NewInt(1)) == 0)

	// 9. 并发只读：64 个 goroutine 读同一个 *Rat，String 逐字节相同。
	shared := must(-6, 8)
	results := make([]string, 64)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = shared.String()
		}(i)
	}
	wg.Wait()
	same := true
	for _, s := range results {
		same = same && s == "-3/4"
	}
	report("concurrency", "64 goroutine 只读结果逐字节相同", same)

	if failed {
		os.Exit(1)
	}
}
