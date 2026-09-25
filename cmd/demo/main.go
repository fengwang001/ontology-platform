// 演示程序：逐项核验多项式求值的正确性、溢出检出与并发一致性。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/eval"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", s, name)
}

func main() {
	cases := []struct {
		coeff []int64
		x     int64
		want  int64
		err   error
	}{
		{[]int64{1, 2, 3}, 2, 17, nil},
		{[]int64{5, -2, 1}, 3, 8, nil},
		{[]int64{0, 0, 1}, 10, 100, nil},
		{[]int64{1, 1, 1, 1}, -2, -5, nil},
		{[]int64{1, 0, 1}, 67108864, 4503599627370497, nil},
		{nil, 5, 0, nil},
		{[]int64{1}, 999, 1, nil},
		{[]int64{0, 3037000500}, 3037000500, 0, eval.ErrMulOverflow},
	}
	ok := true
	for _, c := range cases {
		v, err := api.New(c.coeff).Eval(c.x)
		if !errors.Is(err, c.err) || (err == nil && v != c.want) {
			ok = false
		}
	}
	check("八行表: 17 8 100 -5 4503599627370497 0 1 溢出", ok)

	f := float64(int64(9007199254740992)) // 2^53
	f += 1                                // 2^53+1 不可表示，舍回 2^53（2^52+1 恰可表示，见 NOTES 甲）
	check("float64丢精度: 2^53+1 舍入为 9007199254740992", int64(f) == 9007199254740992)

	_, err := api.New([]int64{0, 3037000500}).Eval(3037000500)
	check("3037000500^2 报乘法溢出", errors.Is(err, eval.ErrMulOverflow))

	v, _ := api.New([]int64{3, 2, 1}).Eval(2)
	check("系数大端误读错值=11(正确17)", v == 11)

	ok = true
	for _, c := range cases[:5] {
		got, _ := api.New(c.coeff).Eval(c.x)
		sum, pow := int64(0), int64(1)
		for _, co := range c.coeff {
			sum += co * pow
			pow *= c.x
		}
		ok = ok && got == sum
	}
	check("Horner 与逐项求和等价", ok)

	e1, _ := api.New(nil).Eval(7)
	e2, _ := api.New([]int64{0, 0, 0}).Eval(-3)
	e3, _ := api.New([]int64{42}).Eval(999)
	check("空/全零/常数正确", e1 == 0 && e2 == 0 && e3 == 42)

	_, aErr := api.New([]int64{1, 1}).Eval(1<<63 - 1)
	_, dErr := api.New(make([]int64, (1<<20)+1)).Eval(1)
	check("三类错误可判定且互不相同",
		errors.Is(err, eval.ErrMulOverflow) && errors.Is(aErr, eval.ErrAddOverflow) &&
			errors.Is(dErr, eval.ErrDegreeLimit) &&
			eval.ErrMulOverflow != eval.ErrAddOverflow && eval.ErrAddOverflow != eval.ErrDegreeLimit)

	p := api.New([]int64{0, 3037000500})
	_, rej := p.Eval(3037000500)
	after, aerr := p.Eval(1)
	check("被拒后无副作用", rej != nil && aerr == nil && after == 3037000500)

	check("乘加次数随次数线性", eval.SelfCheck() && api.New(nil).SelfCheck())
	check("并发只读结果一致", concurrent())

	if failed {
		os.Exit(1)
	}
}

func concurrent() bool {
	p := api.New([]int64{1, 2, 3, 4, 5})
	xs := []int64{0, 1, -1, 2, -3, 7}
	want := make([]int64, len(xs))
	for i, x := range xs {
		want[i], _ = p.Eval(x)
	}
	var wg sync.WaitGroup
	res := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			good := true
			for i, x := range xs {
				v, err := p.Eval(x)
				good = good && err == nil && v == want[i]
			}
			res <- good
		}()
	}
	wg.Wait()
	close(res)
	for good := range res {
		if !good {
			return false
		}
	}
	return true
}
