package main

// 传递闭包演示：不读参数、不联网；逐条 OK/FAIL，任一失败退出码非 0。

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/api"
	"ontology/dg"
)

var specEdges = [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}}
var fails int

func check(name, detail string, ok bool) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}
func specAPI() *api.API {
	a, _ := api.New(5)
	for _, e := range specEdges {
		_ = a.AddEdge(e[0], e[1])
	}
	a.Compute()
	return a
}
func adj(n int, e [][2]int) [][]bool {
	r := make([][]bool, n)
	for i := range r {
		r[i] = make([]bool, n)
	}
	for _, x := range e {
		r[x[0]][x[1]] = true
	}
	return r
}
func fw(r [][]bool) {
	for k := range r {
		for i := range r {
			for j := range r {
				r[i][j] = r[i][j] || r[i][k] && r[k][j]
			}
		}
	}
}

// bad 复现错误实现：mode 0 自反过度；1 跳数 ≤2；2 跳过对角线更新。
func bad(mode int) [][]bool {
	r := adj(5, specEdges)
	if mode == 0 {
		for i := range r {
			r[i][i] = true
		}
	}
	if mode == 1 {
		a := adj(5, specEdges)
		for i := 0; i < 5; i++ {
			for k := 0; k < 5; k++ {
				for j := 0; j < 5; j++ {
					r[i][j] = r[i][j] || a[i][k] && a[k][j]
				}
			}
		}
	}
	if mode != 1 {
		fw(r)
	}
	if mode == 2 {
		for i := range r {
			r[i][i] = false
		}
	}
	return r
}
func scaleOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := api.New(m)
		for i := 0; i < m-1; i++ {
			_ = a.AddEdge(i, i+1)
		}
		a.Compute()
		sink := false
		t0 := time.Now()
		for t := 0; t < 40000; t++ {
			sink = sink || a.Reach(t%m, (t*7+3)%m)
		}
		if !sink || time.Since(t0).Nanoseconds()/40000 > 2000 {
			return false
		}
	}
	return true
}
func concurrencyOK() bool {
	a := specAPI()
	const g = 16
	var rs [g][25]bool
	var wg sync.WaitGroup
	for x := 0; x < g; x++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()
			for t := 0; t < 25; t++ {
				rs[x][t] = a.Reach(t/5, t%5)
			}
		}(x)
	}
	wg.Wait()
	for x := 1; x < g; x++ {
		if rs[x] != rs[0] {
			return false
		}
	}
	return true
}
func main() {
	a := specAPI()
	s := ""
	for t := 0; t < 25; t++ { // 25 位串：行优先的完整闭包矩阵，每 5 位一行
		if a.Reach(t/5, t%5) {
			s += "1"
		} else {
			s += "0"
		}
	}
	check("closure", s+" (rows of 5)", s == "1111111111111110000100000")
	rf, h2, nd := bad(0), bad(1), bad(2)
	check("reflexive Reach(4,4)", "wrong=true correct=false", rf[4][4] && !a.Reach(4, 4))
	check("hop<=2 Reach(0,4)", "wrong=false correct=true", !h2[0][4] && a.Reach(0, 4))
	check("skip-diag Reach(2,2)", "wrong=false correct=true", !nd[2][2] && a.Reach(2, 2))
	_, e0 := api.New(0)
	e1, e2, e3 := a.AddEdge(-1, 0), a.AddEdge(0, 0), a.AddEdge(0, 1)
	d := errors.Is(e0, api.ErrInvalidN) && errors.Is(e1, dg.ErrNodeOutOfRange) &&
		errors.Is(e2, dg.ErrSelfLoop) && errors.Is(e3, dg.ErrDuplicateEdge)
	check("4 distinct sentinels", "invalid-n/out-of-range/self-loop/dup", d)
	check("reject no trace", "count stayed 5, still usable",
		a.EdgeCount() == 5 && a.AddEdge(4, 0) == nil && a.EdgeCount() == 6)
	check("O(1) Reach in m", "m=100..10000 avg<2us", scaleOK())
	check("concurrent reads agree", "16 goroutines x 25 pairs", concurrencyOK())
	if fails > 0 {
		os.Exit(1)
	}
}
