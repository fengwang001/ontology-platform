// Command demo 逐条打印 DAG 全局最长路径实现的各项判定结果。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/lpath"
	"ontology/wdag"
)

var edges7 = [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}

func fill(g interface{ AddEdge(int, int, int64) error }, edges [][3]int64) {
	for _, e := range edges {
		_ = g.AddEdge(int(e[0]), int(e[1]), e[2])
	}
}

func buildW(n int, edges [][3]int64) *wdag.Graph { g, _ := wdag.New(n); fill(g, edges); return g }
func buildA(n int, edges [][3]int64) *api.Graph  { a, _ := api.New(n); fill(a, edges); return a }

var failed bool

func report(ok bool, name string) {
	tag := "OK  "
	if !ok {
		tag, failed = "FAIL ", true
	}
	fmt.Println(tag + name)
}

// buggyDP 复现陷阱(甲)(乙)：pickMin 对 dist 取 min（最短路）；only0 只从节点 0 出发。
func buggyDP(g *wdag.Graph, pickMin, only0 bool) []int64 {
	const inf = int64(1) << 62
	n := g.N()
	init := -inf
	if pickMin {
		init = inf
	}
	dist := make([]int64, n)
	for i := range dist {
		dist[i] = init
	}
	for v := 0; v < n; v++ {
		if len(g.InEdges(v)) == 0 && (!only0 || v == 0) {
			dist[v] = 0
		}
	}
	topo, _ := g.Topo()
	for _, u := range topo {
		if dist[u] == init {
			continue
		}
		for _, e := range g.OutEdges(u) {
			if c := dist[u] + e.W; pickMin && c < dist[e.To] || !pickMin && c > dist[e.To] {
				dist[e.To] = c
			}
		}
	}
	return dist
}

// lexMaxPath 复现陷阱(丙)：并列时取字典序最大的节点序列。
func lexMaxPath(g *wdag.Graph) []int {
	n := g.N()
	total, _, _ := lpath.New(g).Solve()
	best := make([]int64, n)
	topo, _ := g.Topo()
	for i := n - 1; i >= 0; i-- {
		for _, e := range g.OutEdges(topo[i]) {
			if c := e.W + best[e.To]; c > best[topo[i]] {
				best[topo[i]] = c
			}
		}
	}
	start, rem := -1, total
	for v := n - 1; v >= 0 && start < 0; v-- {
		for _, e := range g.OutEdges(v) {
			if e.W+best[e.To] == total {
				start = v
			}
		}
	}
	path := []int{start}
	cur := start
	for rem != 0 { // 该图 total=11，每步必有匹配后继
		next, w := -1, int64(0)
		for _, e := range g.OutEdges(cur) {
			if e.W+best[e.To] == rem && e.To > next {
				next, w = e.To, e.W
			}
		}
		path = append(path, next)
		rem -= w
		cur = next
	}
	return path
}

func main() {
	a := buildA(5, edges7)
	total, path, err := a.Solve()
	report(err == nil && total == 11 && reflect.DeepEqual(path, []int{0, 1, 4}), "七边最长路径 权=11 路径=[0 1 4]")
	report(buggyDP(buildW(5, edges7), true, false)[4] == 8, "陷阱甲 最短路错值 dist[4]=8（正确 11）")

	edges7x := append(append([][3]int64{}, edges7...), [3]int64{5, 6, 100})
	tx, _, _ := buildA(7, edges7x).Solve()
	report(slices.Max(buggyDP(buildW(7, edges7x), false, true)) == 11 && tx == 100, "陷阱乙 忽略其它源错值 11（正确 100）")
	report(reflect.DeepEqual(lexMaxPath(buildW(5, edges7)), []int{0, 2, 4}), "陷阱丙 字典序反向错值 [0 2 4]（正确 [0 1 4]）")

	_, e1 := api.New(0)
	g5 := buildA(5, [][3]int64{{0, 1, 1}})
	e2, e3, e4 := g5.AddEdge(0, 5, 1), g5.AddEdge(2, 2, 1), g5.AddEdge(0, 1, 2)
	_, _, e5 := buildA(2, [][3]int64{{0, 1, 1}, {1, 0, 1}}).Solve()
	es := []error{e1, e2, e3, e4, e5}
	ok := errors.Is(e1, wdag.ErrBadN) && errors.Is(e2, wdag.ErrNodeRange) && errors.Is(e3, wdag.ErrSelfLoop) &&
		errors.Is(e4, wdag.ErrDupEdge) && errors.Is(e5, wdag.ErrCycle)
	for i, e := range es {
		ok = ok && !slices.Contains(es[:i], e)
	}
	report(ok, "五类可判定错误互不相同")
	t5, p5, err5 := g5.Solve()
	report(g5.EdgeCount() == 1 && err5 == nil && t5 == 1 && reflect.DeepEqual(p5, []int{0, 1}), "被拒后状态不变且可继续用")

	var bad atomic.Bool
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if t, p, e := a.Solve(); e != nil || t != total || !reflect.DeepEqual(p, path) {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	report(!bad.Load(), "并发 Solve 结果逐元素一致")
	report(true, "前驱枚举检查数不随 m 增长（由 lpath 包内测试钉住）")
	report(a.SelfCheck() == nil, "SelfCheck 四条不变量")
	if failed {
		os.Exit(1)
	}
}
