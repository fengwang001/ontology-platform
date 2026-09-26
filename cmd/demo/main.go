// Command demo 逐条打印 Pratt 解析器/求值器的自检结果。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/lex"
	"ontology/pratt"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func main() {
	s1, e1 := api.Eval("3^2") // 2^3^2 分步子结果：内层 3^2=9，外层 2^9=512
	s2, e2 := api.Eval("2^9")
	full, e3 := api.Eval("2^3^2")
	check("steps 2^3^2: 3^2=9, 2^9=512", e1 == nil && e2 == nil && e3 == nil &&
		s1 == 9 && s2 == 512 && full == 512)

	vals := map[string]int64{"10-4-3": 3, "2^3^2": 512, "-2^2": 4, "2^10": 1024,
		"- -3": 3, "1- -2": 3, "(1+2)^2": 9}
	ok := true
	for s, want := range vals {
		if v, err := api.Eval(s); err != nil || v != want {
			ok = false
		}
	}
	check("eval: 10-4-3 2^3^2 -2^2 2^10 - -3 1- -2 (1+2)^2", ok)

	eA, eB := errorPair("1&2"), errorPair("(1+2") // 四类可判定错误，互不相同
	eC, eD := errorPair("2^-1"), errorPair("1/0")
	sentinels := []error{lex.ErrIllegalChar, pratt.ErrParen, pratt.ErrBadExp, pratt.ErrDivZero}
	ok = true
	for i, e := range []error{eA, eB, eC, eD} {
		for j, s := range sentinels {
			if errors.Is(e, s) != (i == j) {
				ok = false
			}
		}
	}
	check("errors: illegal/paren/badexp/divzero distinct", ok)

	v, err := api.Eval("6*7") // 被拒后状态不变
	check("state: 6*7=42 after rejections", err == nil && v == 42)

	ok = true // 大 m 下高 min_bp 层提前停止（比较个数的证明在 pratt 包内测试）
	for _, m := range []int{100, 1000, 10000} {
		if _, err := api.Parse("-" + strings.Repeat("2^", m) + "2"); err != nil {
			ok = false
		}
	}
	check("early-stop: parse -(2^...^2) m=100..10000", ok)

	exprs := []string{"2^3^2", "-2^2", "(1+2)^2", "10-4-3", "2^10", "1- -2"}
	results := make([][]int64, len(exprs)) // 并发求值逐值一致
	for i := range results {
		results[i] = make([]int64, 32)
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, s := range exprs {
				v, err := api.Eval(s)
				if err != nil {
					results[i][g] = -1 << 62
					return
				}
				results[i][g] = v
			}
		}(g)
	}
	wg.Wait()
	ok = true
	for i, s := range exprs {
		for g := 0; g < 32; g++ {
			if results[i][g] != vals[s] {
				ok = false
			}
		}
	}
	check("concurrency: 32 goroutines identical", ok)

	check("selfcheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

func errorPair(s string) error {
	_, err := api.Eval(s)
	return err
}
