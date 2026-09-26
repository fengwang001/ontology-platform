package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/topo"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// dag 包：三类非法边可判定且互不相同，被拒后状态不变、图仍可用。
	g := dag.New(3)
	if err := g.AddEdge(0, 1); err != nil {
		check("dag.AddEdge", false)
	}
	e1, e2, e3 := g.AddEdge(1, 1), g.AddEdge(0, 3), g.AddEdge(0, 1)
	check("dag.三类非法边可判定", errors.Is(e1, dag.ErrSelfLoop) &&
		errors.Is(e2, dag.ErrNodeOutOfRange) && errors.Is(e3, dag.ErrDuplicateEdge) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	check("dag.状态不变", g.EdgeCount() == 1 && g.AddEdge(1, 2) == nil && g.EdgeCount() == 2)

	// topo 包：第三节七条边的拓扑序、两个陷阱错值、含环哨兵与部分序列。
	seven := func(extra ...[2]int) *dag.Graph {
		g := dag.New(7)
		for _, e := range append([][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}}, extra...) {
			_ = g.AddEdge(e[0], e[1])
		}
		return g
	}
	order, err := topo.Sort(seven())
	check("topo.七边拓扑序", err == nil && reflect.DeepEqual(order, []int{2, 0, 3, 4, 1, 5, 6}))
	check("topo.FIFO陷阱第2个错成3", fifoOrder(seven())[1] == 3)
	check("topo.最大优先陷阱第1个错成5", scanPartial(seven(), true)[0] == 5)
	cyc := seven([2]int{0, 2})
	_, err = topo.Sort(cyc)
	check("topo.ErrCycle", errors.Is(err, topo.ErrCycle))
	check("topo.不检测环的部分序列[3,4,5]", reflect.DeepEqual(scanPartial(cyc, false), []int{3, 4, 5}))

	// api 包：自检、大 m 规模、并发一致。
	check("api.SelfCheck", api.SelfCheck() == nil)
	big := api.New(10000) // m 个互不相连节点；检查数不随 m 增长的断言在 topo 包测试
	bigOrder, bigErr := big.TopoSort()
	check("api.大m拓扑序完整", bigErr == nil && len(bigOrder) == 10000 && bigOrder[0] == 0 && bigOrder[9999] == 9999)
	check("api.并发TopoSort逐元素一致", concurrentSame(seven(), order))

	if failed {
		os.Exit(1)
	}
}

// concurrentSame 用 32 个 goroutine 并发排序同一 DAG，要求结果逐元素相同。
func concurrentSame(g *dag.Graph, want []int) bool {
	var wg sync.WaitGroup
	same := make([]bool, 32)
	for i := range same {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := topo.Sort(g)
			same[i] = err == nil && reflect.DeepEqual(got, want)
		}(i)
	}
	wg.Wait()
	for _, ok := range same {
		if !ok {
			return false
		}
	}
	return true
}

// fifoOrder 错误实现参照：初始入度 0 节点升序入队，新归零节点追加队尾。
func fifoOrder(g *dag.Graph) []int {
	indeg, adj := g.Snapshot()
	var q []int
	for v, d := range indeg {
		if d == 0 {
			q = append(q, v)
		}
	}
	var out []int
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		out = append(out, v)
		for _, w := range adj[v] {
			if indeg[w]--; indeg[w] == 0 {
				q = append(q, w)
			}
		}
	}
	return out
}

// scanPartial 错误实现参照：全表扫描取入度 0 节点，maxFirst 为真取最大编号
// （否则最小编号）；不检测环，扫不动就静默返回部分序列。
func scanPartial(g *dag.Graph, maxFirst bool) []int {
	indeg, adj := g.Snapshot()
	var out []int
	for {
		v := -1
		for i, d := range indeg {
			if d == 0 && (v < 0 || (maxFirst && i > v) || (!maxFirst && i < v)) {
				v = i
			}
		}
		if v < 0 {
			return out
		}
		indeg[v] = -1
		out = append(out, v)
		for _, w := range adj[v] {
			indeg[w]--
		}
	}
}
