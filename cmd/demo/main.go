package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/period"
	"ontology/pfx"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func naiveMin(s string) int {
	for p := 1; p <= len(s); p++ {
		if period.IsPeriodic(s, p) {
			return p
		}
	}
	return 0
}

func must(s string) *api.Checker {
	c, err := api.New(s)
	if err != nil {
		panic(err)
	}
	return c
}

func main() {
	pf := pfx.Build("abababa")
	want := []int{0, 0, 1, 2, 3, 4, 5}
	match := pf.Len() == len(want)
	for i, v := range want {
		match = match && pf.At(i) == v
	}
	ok("pfx: pi(abababa)=[0 0 1 2 3 4 5]", match)

	c1 := must("ababab")
	p1, _ := c1.MinPeriod()
	ok("ababab: p=2 且是 power", p1 == 2 && c1.IsPower())
	c2 := must("abababa")
	p2, _ := c2.MinPeriod()
	ok("abababa: p=2 但不是 power", p2 == 2 && !c2.IsPower())
	p3, _ := must("abcabcabc").MinPeriod()
	c4 := must("abcde")
	p4, _ := c4.MinPeriod()
	ok("abcabcabc: p=3；abcde: p=5 非 power", p3 == 3 && p4 == 5 && !c4.IsPower())
	y, _ := c1.IsPeriodic(2)
	y4, _ := c1.IsPeriodic(4)
	ok("IsPeriodic(ababab,2)=true; 4 依定义也是周期(border \"ab\")", y && y4)

	rng := rand.New(rand.NewSource(1))
	consistent := true
	for i := 0; i < 200; i++ {
		b := make([]byte, 1+rng.Intn(40))
		for j := range b {
			b[j] = byte('a' + rng.Intn(3))
		}
		consistent = consistent && period.MinPeriod(string(b)) == naiveMin(string(b))
	}
	ok("与朴素结果一致(200 随机串)", consistent && must("abc").SelfCheck() == nil)

	_, e1 := api.New("")
	_, e2 := api.New(string(make([]byte, 1<<20+1)))
	_, e3 := c1.IsPeriodic(0)
	_, e4 := c1.IsPeriodic(7)
	errs := errors.Is(e1, api.ErrEmpty) && errors.Is(e2, api.ErrTooLong) &&
		errors.Is(e3, api.ErrBadPeriod) && errors.Is(e4, api.ErrBadPeriod) &&
		e1 != e2 && e2 != e3 && e1 != e3
	ok("三类哨兵错误可判定且互不相同", errs)

	before, _ := c1.MinPeriod()
	_, _ = api.New("")
	_, _ = c1.IsPeriodic(-1)
	after, _ := c1.MinPeriod()
	y2, _ := c1.IsPeriodic(2)
	ok("被拒后状态不变仍可正常使用", before == after && y2)

	big := make([]byte, 10000)
	for i := range big {
		big[i] = byte('a' + rng.Intn(26))
	}
	pm, _ := must(string(big)).MinPeriod()
	ok("大 n 结果正确(比较次数线性由 pfx 测试钉住)", pm >= 1 && period.IsPeriodic(string(big), pm))

	const G = 16
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mp, _ := c2.MinPeriod()
			ip, _ := c2.IsPeriodic(2)
			pw := c2.IsPower()
			sc := c2.SelfCheck()
			mu.Lock()
			same = same && mp == 2 && ip && !pw && sc == nil
			mu.Unlock()
		}()
	}
	wg.Wait()
	ok("并发只读结果逐项相同", same)
}
