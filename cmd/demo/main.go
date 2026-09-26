// demo 依次核验：第三节 idom、两个陷阱错值、不可达哨兵、四类错误、
// 状态不变、深链 O(1) 查询、并发一致、SelfCheck。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func main() {
	e, _ := api.New(8) // 节点 7 无边：不可达
	for _, ed := range [][2]int{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}} {
		e.AddEdge(ed[0], ed[1])
	}
	e.Compute()
	ids := make([]int, 7)
	for v := range ids {
		ids[v], _ = e.IDom(v)
	}
	check(fmt.Sprintf("idom[0..6]=%v", ids), fmt.Sprint(ids) == "[0 0 1 2 2 2 0]")
	// 陷阱错值：DFS 父节点会把 idom(6) 错成 1；只取首前驱会把 idom(5) 错成 3
	check("陷阱: idom(6)=0(非DFS父1), idom(5)=2(非首前驱3)", ids[6] == 0 && ids[5] == 2)
	id7, _ := e.IDom(7)
	check("不可达: IDom(7)=-1, Dominates(0,7)=false", id7 == -1 && !e.Dominates(0, 7) && !e.Dominates(7, 7))
	_, errN := api.New(0)
	e3, _ := api.New(3)
	e3.AddEdge(0, 1)
	seen := map[error]bool{}
	for _, err := range []error{errN, e3.AddEdge(0, 9), e3.AddEdge(2, 2), e3.AddEdge(0, 1)} {
		seen[err] = true
	}
	check("四类可判定错误互不相同", len(seen) == 4 && !seen[nil])
	check("被拒后状态不变且可用", e3.EdgeCount() == 1)
	ok := true
	for _, m := range []int{100, 1000, 10000} { // 检查个数上界由 dom 包测试钉住
		c, _ := api.New(m)
		for i := 0; i+1 < m; i++ {
			c.AddEdge(i, i+1)
		}
		c.Compute()
		ok = ok && c.Dominates(0, m-1)
	}
	check("深链 Dominates(0,m-1) m=100..10000", ok)
	pairs := [][2]int{{0, 6}, {1, 6}, {2, 5}, {3, 5}, {0, 0}, {6, 6}, {0, 7}}
	want := make([]bool, len(pairs))
	for i, p := range pairs {
		want[i] = e.Dominates(p[0], p[1])
	}
	got := make([][]bool, 64)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]bool, len(pairs))
			for i, p := range pairs {
				got[g][i] = e.Dominates(p[0], p[1])
			}
		}(g)
	}
	wg.Wait()
	ok = true
	for g := range got {
		for i := range want {
			ok = ok && got[g][i] == want[i]
		}
	}
	check("64 goroutine 并发查询逐对一致", ok)
	check("SelfCheck", e.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
