package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/sw"
	"ontology/wg"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed = true
	}
	fmt.Println(s, name)
}

// trapSW is a deliberately buggy Stoer-Wagner exhibiting the traps of NOTES.md:
// mode 0 = last phase only, mode 1 = MAS picks minimum, mode 2 = s-t edge as cut.
func trapSW(n int, edges [][3]int64, mode int) int64 {
	adj := make([][]int64, n)
	alive := make([]bool, n)
	for i := range adj {
		adj[i] = make([]int64, n)
		alive[i] = true
	}
	for _, e := range edges {
		adj[e[0]][e[1]] += e[2]
		adj[e[1]][e[0]] += e[2]
	}
	best, last := int64(-1), int64(0)
	for left := n; left > 1; left-- {
		inA, w := make([]bool, n), make([]int64, n)
		s, t := 0, 0
		for k := 0; k < left; k++ {
			p := -1
			for v := 0; v < n; v++ {
				if alive[v] && !inA[v] && (p < 0 || (mode == 1 && w[v] < w[p]) || (mode != 1 && w[v] > w[p])) {
					p = v
				}
			}
			inA[p] = true
			s, t = t, p
			for x := 0; x < n; x++ {
				if alive[x] && !inA[x] {
					w[x] += adj[p][x]
				}
			}
		}
		cut := w[t]
		if mode == 2 {
			cut = adj[s][t]
		}
		last = cut
		if best < 0 || cut < best {
			best = cut
		}
		if t < s {
			s, t = t, s
		}
		for x := 0; x < n; x++ {
			if x != s && alive[x] {
				adj[s][x] += adj[t][x]
				adj[x][s] += adj[x][t]
			}
		}
		alive[t] = false
	}
	if mode == 0 {
		return last
	}
	return best
}

func main() {
	// sw 包：第三节四边图的最小割、三个阶段割值、三个陷阱错值。
	edges := [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}}
	g4, _ := wg.New(4)
	for _, e := range edges {
		_ = g4.AddEdge(int(e[0]), int(e[1]), e[2])
	}
	check("mincut(4-edge graph)==1", sw.MinCut(g4) == 1)
	ph := sw.Run(g4)
	check("phase cuts==[1 6 8]", len(ph) == 3 && ph[0].Cut == 1 && ph[1].Cut == 6 && ph[2].Cut == 8)
	check("trap last-phase-only==8", trapSW(4, edges, 0) == 8)
	check("trap min-MAS==8", trapSW(4, edges, 1) == 8)
	check("trap st-edge-cut==0", trapSW(4, edges, 2) == 0)

	// wg 包：四类可判定错误 + 失败不留痕。
	_, e1 := wg.New(1)
	g, _ := wg.New(3)
	e2 := g.AddEdge(0, 3, 1)
	e3 := g.AddEdge(1, 1, 1)
	_ = g.AddEdge(0, 1, 5)
	e4 := g.AddEdge(1, 0, 7)
	seen := map[error]bool{}
	ok := true
	for _, e := range []error{e1, e2, e3, e4} {
		ok, seen[e] = ok && e != nil && !seen[e], true
	}
	ok = ok && errors.Is(e1, wg.ErrTooFewNodes) && errors.Is(e2, wg.ErrNodeOutOfRange) &&
		errors.Is(e3, wg.ErrSelfLoop) && errors.Is(e4, wg.ErrDuplicateEdge)
	check("4 distinct sentinel errors", ok)
	check("rejected ops leave state unchanged", g.EdgeCount() == 1 && g.AddEdge(1, 2, 4) == nil && g.EdgeCount() == 2)

	// api 包：链图割值不随 m 变（MAS 候选检查数 ≤2 由 sw 白盒测试钉住）。
	ok = true
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		cg, _ := api.New(m)
		for i := 0; i+1 < m; i++ {
			_ = cg.AddEdge(i, i+1, 1)
		}
		mc, _ := cg.MinCut()
		ok = ok && mc == 1
	}
	check("chain mincut==1 for m=100..10000", ok)
	// api 包：并发 MinCut 结果一致。
	ag, _ := api.New(4)
	for _, e := range edges {
		_ = ag.AddEdge(int(e[0]), int(e[1]), e[2])
	}
	var wg2 sync.WaitGroup
	bad := make(chan int64, 64)
	for i := 0; i < 64; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for j := 0; j < 50; j++ {
				if mc, _ := ag.MinCut(); mc != 1 {
					bad <- mc
				}
			}
		}()
	}
	wg2.Wait()
	close(bad)
	check("concurrent MinCut consistent", len(bad) == 0)
	sc, _ := api.New(2)
	check("SelfCheck()==nil", sc.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
