// Command demo 校验 zfn/period/api 三个包，逐条打印 OK/FAIL，有 FAIL 则退出码非 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/zfn"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		fmt.Println("FAIL: " + name)
		failures++
	}
}

func naivePeriod(rs []rune) int {
	for p := 1; p < len(rs); p++ {
		ok := true
		for i := 0; i < len(rs)-p; i++ {
			if rs[i] != rs[i+p] {
				ok = false
				break
			}
		}
		if ok {
			return p
		}
	}
	return len(rs)
}

func main() {
	var a api.API
	if err := a.New("ababa"); err != nil {
		fmt.Println("FAIL: build ababa:", err)
		os.Exit(1)
	}
	zz, _ := a.Z()
	pp, _ := a.Period()
	check("ababa Z=[0 0 3 0 1] and period=2",
		reflect.DeepEqual(zz, []int{0, 0, 3, 0, 1}) && pp == 2)

	naiveOK := true
	for _, s := range []string{"aaaa", "ababab", "aabaaaba", "αβαβα", "mississippi"} {
		var b api.API
		if err := b.New(s); err != nil {
			naiveOK = false
			continue
		}
		r := []rune(s)
		g, _ := b.Z()
		if g[0] != 0 {
			naiveOK = false
		}
		for i := range g {
			if g[i] < 0 || g[i] > len(r)-i {
				naiveOK = false
			}
		}
		if q, _ := b.Period(); q != naivePeriod(r) {
			naiveOK = false
		}
	}
	check("Z bounds and period match naive", naiveOK)

	var c api.API
	e1 := c.New("")
	var bad *api.InvalidUTF8Error
	e2 := c.New("a" + string([]byte{0xff}))
	var zero api.API
	_, eNotBuilt := zero.Z()
	check("three distinct sentinel errors",
		errors.Is(e1, api.ErrEmpty) && errors.Is(e2, api.ErrInvalidUTF8) &&
			errors.As(e2, &bad) && bad.Offset == 1 &&
			errors.Is(eNotBuilt, api.ErrNotBuilt) && !errors.Is(e1, api.ErrNotBuilt))

	_, still := c.Z()
	traceOK := errors.Is(still, api.ErrNotBuilt) && c.New("ababa") == nil
	if g, _ := c.Z(); !reflect.DeepEqual(g, []int{0, 0, 3, 0, 1}) {
		traceOK = false
	}
	check("rejected ops leave no trace", traceOK)

	rng := rand.New(rand.NewSource(7))
	budgetOK := true
	for _, n := range []int{100, 1000, 10000} {
		forms := [][]rune{
			[]rune(strings.Repeat("a", n)),
			[]rune(strings.Repeat("aab", n/3+1))[:n],
		}
		rnd := make([]rune, n)
		for i := range rnd {
			rnd[i] = []rune("ab")[rng.Intn(2)]
		}
		for _, f := range append(forms, rnd) {
			if zfn.New(f).CheckLinear() != nil {
				budgetOK = false
			}
		}
	}
	check("comparisons <= 2n up to n=10000", budgetOK)

	const N = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	zs := make([][]int, N)
	ps := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			zs[i], _ = a.Z()
			ps[i], _ = a.Period()
		}(i)
	}
	close(start)
	wg.Wait()
	concOK := true
	for i := 1; i < N; i++ {
		if ps[i] != ps[0] || !reflect.DeepEqual(zs[i], zs[0]) {
			concOK = false
		}
	}
	check("concurrent Z/Period identical", concOK)
	check("SelfCheck passes", a.SelfCheck() == nil)

	if failures > 0 {
		os.Exit(1)
	}
}
