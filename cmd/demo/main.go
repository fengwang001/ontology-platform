package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/topo"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

var sevenEdges = [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}}

func sevenGraph(extra ...[2]int) *dag.Graph {
	g := dag.New(7)
	for _, e := range append(sevenEdges, extra...) {
		g.AddEdge(e[0], e[1])
	}
	return g
}

// trapOrder 用 mode 指定的错误策略排序：0=FIFO，1=最大编号优先，2=最小优先但不检测环。
func trapOrder(g *dag.Graph, mode int) []int {
	n := g.N()
	indeg := make([]int, n)
	var cand []int
	for v := 0; v < n; v++ {
		indeg[v] = g.Indeg(v)
		if indeg[v] == 0 {
			cand = append(cand, v)
		}
	}
	var out []int
	for len(cand) > 0 {
		i := 0
		for j := range cand { // mode 1 取最大，mode 2 取最小，mode 0 取队首
			if mode == 1 && cand[j] > cand[i] || mode == 2 && cand[j] < cand[i] {
				i = j
			}
		}
		u := cand[i]
		cand = append(cand[:i], cand[i+1:]...)
		out = append(out, u)
		for _, v := range g.Adj(u) {
			indeg[v]--
			if indeg[v] == 0 {
				cand = append(cand, v)
			}
		}
	}
	return out
}

func main() {
	// dag：三类加边错误可判定、互不相同，被拒后状态不变
	g := dag.New(3)
	e1 := g.AddEdge(0, 0)
	e2 := g.AddEdge(0, 5)
	okEdge := g.AddEdge(0, 1) == nil
	e3 := g.AddEdge(0, 1)
	distinct := errors.Is(e1, dag.ErrSelfLoop) && errors.Is(e2, dag.ErrNodeRange) &&
		errors.Is(e3, dag.ErrDupEdge) && !errors.Is(e1, dag.ErrDupEdge) &&
		!errors.Is(e2, dag.ErrSelfLoop) && !errors.Is(e3, dag.ErrNodeRange)
	check("dag: 三类加边错误可判定且互不相同", okEdge && distinct)
	check("dag: 被拒操作不留痕", g.EdgeCount() == 1 && g.Indeg(1) == 1)

	// topo：第三节七条边的拓扑序
	got, err := new(topo.Sorter).Sort(sevenGraph())
	check(fmt.Sprintf("topo: 七边拓扑序 %v", got), err == nil && equal(got, []int{2, 0, 3, 4, 1, 5, 6}))

	// 陷阱：(甲) FIFO 第 2 个输出错成 3（正确 0）；(乙) 最大优先第 1 个错成 5（正确 2）
	fifo := trapOrder(sevenGraph(), 0)
	mx := trapOrder(sevenGraph(), 1)
	check(fmt.Sprintf("topo: FIFO 第2个=%d(应0) 最大优先第1个=%d(应2)", fifo[1], mx[0]),
		fifo[1] == 3 && mx[0] == 5)

	// 含环：返回 ErrCycle 且无部分序列；不检测环的错实现返回 [3 4 5]
	cyc := sevenGraph([2]int{0, 2})
	out, err := new(topo.Sorter).Sort(cyc)
	partial := trapOrder(cyc, 2)
	check(fmt.Sprintf("topo: 含环哨兵=%v 不检测时部分序列=%v", err, partial),
		errors.Is(err, dag.ErrCycle) && out == nil && equal(partial, []int{3, 4, 5}))

	// api：SelfCheck 四条不变量；大 m 排序（检查数不随 m 增长由 topo 包内测试钉住）
	a := api.New(1)
	check("api: SelfCheck 四条不变量", a.SelfCheck() == nil)
	big := api.New(10000)
	bigOut, bigErr := big.TopoSort()
	check("api: m=10000 孤立节点排序", bigErr == nil && len(bigOut) == 10000)

	// 并发：16 个 goroutine 并发 TopoSort，结果逐元素一致
	c := api.New(64)
	for u := 0; u < 64; u++ {
		for v := u + 1; v < 64; v++ {
			if (u+v)%3 == 0 {
				c.AddEdge(u, v)
			}
		}
	}
	want, _ := c.TopoSort()
	start := make(chan struct{})
	res := make([][]int, 16)
	var wg sync.WaitGroup
	for p := range res {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			r, _ := c.TopoSort()
			res[p] = r
		}(p)
	}
	close(start)
	wg.Wait()
	same := true
	for _, r := range res {
		same = same && equal(r, want)
	}
	check("api: 并发 TopoSort 逐元素一致", same)

	if failed {
		os.Exit(1)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
