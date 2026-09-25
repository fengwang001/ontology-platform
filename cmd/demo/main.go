package main

import (
	"errors"
	"fmt"

	"ontology/align"
	"ontology/api"
	"sync"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// align 包：对齐计算与请求校验（含第三节(甲)(丙)的判定值）。
	alignOK := align.Up(41, 8) == 48 && align.Up(8, 16) == 16 &&
		align.HeaderOff(16) == 8 &&
		errors.Is(align.Check(0, 8), align.ErrInvalidSize) &&
		errors.Is(align.Check(4, 6), align.ErrInvalidAlign) &&
		align.Check(4, 8) == nil
	report("align: up/headerOff/validate", alignOK)

	// alloc 包（经 api 对外）：第三节八步操作，逐步核对 ptr 与对齐。
	a := api.New()
	type op struct {
		size, al, want int
	}
	allocs := []op{{10, 16, 16}, {4, 8, 48}, {8, 8, 64}, {1, 8, 88}, {16, 16, 112}}
	var ptrs []int
	traceOK := true
	for i, o := range allocs {
		p, err := a.Alloc(o.size, o.al)
		if err != nil || p != o.want || p%o.al != 0 {
			traceOK = false
		}
		ptrs = append(ptrs, p)
		if i == 2 { // 第 4、5 步：Free(16)、Free(48)
			for _, fp := range []int{16, 48} {
				if err := a.Free(fp); err != nil {
					traceOK = false
				}
			}
		}
	}
	if err := a.Free(64); err != nil { // 第 8 步
		traceOK = false
	}
	report("trace8: ptr per step + aligned", traceOK)
	report("alloc: SelfCheck (base/next/invariants/O(1))", a.SelfCheck() == nil)

	// api 包：三类可判定错误互不相同，且被拒后状态不变、可继续正常使用。
	before := a.Allocated()
	_, e1 := a.Alloc(-1, 8)
	_, e2 := a.Alloc(4, 6)
	e3 := a.Free(123456)
	errOK := errors.Is(e1, api.ErrInvalidSize) && errors.Is(e2, api.ErrInvalidAlign) &&
		errors.Is(e3, api.ErrBadFree) && e1 != e2 && e2 != e3 && e1 != e3
	report("errors: 3 distinct sentinel kinds", errOK)
	stableOK := a.Allocated() == before
	if p, err := a.Alloc(1, 8); err != nil || p%8 != 0 { // 被拒后仍可正常使用
		stableOK = false
	}
	report("state unchanged after rejections", stableOK)

	// 并发：N 个 goroutine 各 Alloc 一次，指针互异且各自对齐。
	const n = 256
	type res struct{ ptr, al int }
	ptrCh := make(chan res, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(al int) {
			defer wg.Done()
			if p, err := a.Alloc(8, al); err == nil {
				ptrCh <- res{p, al}
			}
		}(1 << (g % 5)) // align ∈ {1,2,4,8,16}
	}
	wg.Wait()
	close(ptrCh)
	seen := map[int]bool{}
	concOK := true
	cnt := 0
	for r := range ptrCh {
		if seen[r.ptr] || r.ptr%r.al != 0 {
			concOK = false
		}
		seen[r.ptr] = true
		cnt++
	}
	concOK = concOK && cnt == n && a.Allocated() == before+1+n
	report("concurrent allocs: unique + aligned", concOK)

	if failed {
		fmt.Println("DEMO FAIL")
	} else {
		fmt.Println("ALL OK")
	}
}
