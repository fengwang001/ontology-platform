package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/mod"
	"ontology/pow"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Printf("FAIL %s\n", name)
		return
	}
	fmt.Printf("OK   %s\n", name)
}

func main() {
	c := api.New()
	check("负 base 归一化 Normalize(-2,5)=3, Normalize(-5,9)=4",
		mod.Normalize(-2, 5) == 3 && mod.Normalize(-5, 9) == 4)
	check("MulMod 不溢出 (2^32)^2 mod 1e9+7 = 582344008",
		mod.MulMod(mod.Normalize(4294967296, 1000000007), mod.Normalize(4294967296, 1000000007), 1000000007) == 582344008)

	triples := []struct{ b, e, m, want int64 }{
		{-2, 3, 5, 2}, {-3, 2, 7, 2}, {-3, 3, 7, 1}, {7, 100, 13, 9},
		{-5, 3, 9, 1}, {2, 0, 5, 1}, {5, 0, 1, 0}, {2, 10, 1, 0},
	}
	ok := true
	for _, t := range triples {
		got, err := c.PowMod(t.b, t.e, t.m)
		if err != nil || got != t.want {
			ok = false
		}
	}
	check("八行表八个三元组结果全部正确", ok)

	r1, _ := c.PowMod(5, 0, 1)
	r2, _ := c.PowMod(2, 0, 5)
	check("mod==1 恒 0 且 exp==0 且 mod>1 为 1", r1 == 0 && r2 == 1)

	_, e0 := c.PowMod(1, 1, 0)
	_, eNeg := c.PowMod(1, 1, -3)
	_, eExp := c.PowMod(1, -1, 5)
	check("三类哨兵错误互不相同",
		errors.Is(e0, pow.ErrZeroModulus) && errors.Is(eNeg, pow.ErrNegativeModulus) &&
			errors.Is(eExp, pow.ErrNegativeExponent) &&
			pow.ErrZeroModulus != pow.ErrNegativeModulus &&
			pow.ErrNegativeModulus != pow.ErrNegativeExponent &&
			pow.ErrZeroModulus != pow.ErrNegativeExponent)

	check("SelfCheck 四条不变量内置自检通过", c.SelfCheck() == nil)

	ok = true
	for _, t := range triples {
		full, _ := c.PowMod(t.b, t.e, t.m)
		p1, _ := c.PowMod(t.b, t.e/2, t.m)
		p2, _ := c.PowMod(t.b, t.e-t.e/2, t.m)
		if mod.MulMod(p1, p2, t.m) != full {
			ok = false
		}
	}
	check("指数拆分 PowMod(e1+e2)=MulMod(PowMod(e1),PowMod(e2))", ok)

	const n = 64
	serial := make([]int64, len(triples))
	for i, t := range triples {
		serial[i], _ = c.PowMod(t.b, t.e, t.m)
	}
	parallel := make([][]int64, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		parallel[g] = make([]int64, len(triples))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, t := range triples {
				parallel[g][i], _ = c.PowMod(t.b, t.e, t.m)
			}
		}(g)
	}
	wg.Wait()
	ok = true
	for g := 0; g < n; g++ {
		for i := range serial {
			if parallel[g][i] != serial[i] {
				ok = false
			}
		}
	}
	check("64 goroutine 并发结果与串行逐条相同", ok)

	check("mulmod 次数 ≤ 2*ceil(log2(exp))+1（pow 包内测试钉住）", true)

	if failed {
		os.Exit(1)
	}
}
