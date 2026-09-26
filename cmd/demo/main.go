package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/bloom"
	"ontology/hashk"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// hashk：NOTES.md 八行表的金值位置（m=10, k=3）
	golden := map[string][]int{
		"a": {7, 5, 3}, "b": {8, 7, 6}, "c": {9, 0, 1},
		"o": {1, 5, 9}, "d": {0, 2, 4}, "x": {0, 4, 8},
	}
	ok := true
	for s, want := range golden {
		got := hashk.Positions([]byte(s), 10, 3)
		for j := range want {
			if got[j] != want[j] {
				ok = false
			}
		}
	}
	check("hashk positions match NOTES table", ok)

	// bloom：第三节八个操作，位数组与 Test 结果
	f, _ := bloom.New(10, 3, 8)
	f.Add([]byte("a"))
	f.Add([]byte("b"))
	f.Add([]byte("c"))
	bits := f.BitString()
	results := []bool{}
	for _, s := range []string{"o", "a", "d", "b", "x"} {
		results = append(results, f.Test([]byte(s)))
	}
	wantBits := "1101011111" // 位 0..9：{0,1,3,5,6,7,8,9} 为 1
	wantRes := []bool{true, true, false, true, false}
	ok = bits == wantBits && len(results) == len(wantRes)
	for i := range wantRes {
		ok = ok && results[i] == wantRes[i]
	}
	fmt.Printf("bits=%s tests(o,a,d,b,x)=%v\n", bits, results)
	check("eight ops: bits and Test results match NOTES", ok)

	// bloom：无假阴性
	f2, _ := bloom.New(1024, 5, 100)
	ok = true
	for i := 0; i < 100; i++ {
		x := []byte(fmt.Sprintf("elem-%d", i))
		if f2.Add(x) != nil || !f2.Test(x) {
			ok = false
		}
	}
	check("no false negatives on 100 elements", ok)

	// bloom：检查位数恒为 k（O(k) 定位）
	check("probe cost stays k for n=100..10000", bloom.ProbeCostVerified(3))

	// api：三类可判定错误互不相同，且被拒后状态不变
	if _, e1 := api.New(0, 3, 10); !errors.Is(e1, api.ErrBadParam) {
		check("sentinel ErrBadParam", false)
	}
	g, _ := api.New(64, 3, 1)
	e2 := g.Add(nil)
	_ = g.Add([]byte("x"))
	e3 := g.Add([]byte("y"))
	ok = errors.Is(e2, api.ErrEmpty) && errors.Is(e3, api.ErrFull) &&
		!errors.Is(e2, api.ErrFull) && !errors.Is(e3, api.ErrEmpty) &&
		!errors.Is(e2, api.ErrBadParam) && !errors.Is(e3, api.ErrBadParam)
	check("three sentinel errors distinct", ok)
	ok = g.Count() == 1
	if v, _ := g.Test([]byte("x")); !v {
		ok = false
	}
	if v, _ := g.Test([]byte("y")); v {
		ok = false
	}
	check("state unchanged after rejections", ok)

	// api：SelfCheck 覆盖四条不变量
	check("api.SelfCheck passes", api.SelfCheck() == nil)

	// 并发：N 个 goroutine 并发 Add 不同元素 + N 个并发 Test 同一元素
	h, _ := api.New(1<<20, 5, 256)
	var wg sync.WaitGroup
	for i := 0; i < 128; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); _ = h.Add([]byte(fmt.Sprintf("c-%d", i))) }(i)
		go func() { defer wg.Done(); _, _ = h.Test([]byte("shared")) }()
	}
	wg.Wait()
	ok = h.Count() == 128
	for i := 0; i < 128; i++ {
		if v, _ := h.Test([]byte(fmt.Sprintf("c-%d", i))); !v {
			ok = false
		}
	}
	check("concurrent Add/Test consistent, Count==128", ok)

	if failed {
		os.Exit(1)
	}
}
