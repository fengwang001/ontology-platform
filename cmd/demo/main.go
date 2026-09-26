package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"ontology/api"
	"ontology/dg"
	"ontology/tc"
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

// 四类可判定错误互不相同；被拒后状态不变、仍可正常使用。
func checkErrors() {
	seen := map[error]bool{}
	if _, err := dg.New(0); errors.Is(err, dg.ErrNonPositiveN) {
		seen[err] = true
	}
	g, _ := dg.New(3)
	_ = g.AddEdge(0, 1)
	before := g.EdgeCount()
	uv := [][2]int{{0, 3}, {1, 1}, {0, 1}}
	want := []error{dg.ErrNodeOutOfRange, dg.ErrSelfLoop, dg.ErrDuplicateEdge}
	ok := true
	for k := range uv {
		if err := g.AddEdge(uv[k][0], uv[k][1]); !errors.Is(err, want[k]) {
			ok = false
		}
		seen[want[k]] = true
	}
	report("errors: 4 distinct sentinel errors", ok && len(seen) == 4)
	ok = g.EdgeCount() == before && !g.HasEdge(1, 2)
	if err := g.AddEdge(1, 2); err != nil || !g.HasEdge(1, 2) {
		ok = false
	}
	report("state unchanged after rejections", ok)
}

// 第三节五条边的完整闭包矩阵 + 三个陷阱对应的错值。
func checkClosure() {
	g, _ := dg.New(5)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}} {
		_ = g.AddEdge(e[0], e[1])
	}
	c := tc.Compute(g)
	want := [5][5]bool{
		{true, true, true, true, true},
		{true, true, true, true, true},
		{true, true, true, true, true},
		{false, false, false, false, true},
		{false, false, false, false, false},
	}
	ok := true
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			ok = ok && c.Reach(i, j) == want[i][j]
		}
	}
	report("closure matrix of 5-edge graph", ok)
	// (甲) 自反过度会把 Reach(4,4) 错判成 true，正确为 false。
	report("trap reflexive: Reach(4,4)=false (buggy:true)", !c.Reach(4, 4))
	// (乙) 跳数≤2 会把 Reach(0,4) 漏判成 false，正确为 true。
	report("trap hop<=2: Reach(0,4)=true (buggy:false)", c.Reach(0, 4))
	// (丙) 跳过对角线会把 Reach(2,2) 错判成 false，正确为 true。
	report("trap skip-diag: Reach(2,2)=true (buggy:false)", c.Reach(2, 2))
}

// 大 m 下 Reach 单位查询耗时不随 m 增长（O(1) 查表，而非每次 DFS 扫 O(m) 个节点）。
func checkScaling() {
	measure := func(m int) time.Duration {
		a, _ := api.New(m)
		for i := 1; i < m; i++ { // 星形：从 0 出发的 DFS 要扫 O(m) 个节点
			_ = a.AddEdge(0, i)
		}
		a.Compute()
		best := time.Hour
		for r := 0; r < 3; r++ {
			start := time.Now()
			for k := 0; k < 100000; k++ {
				a.Reach(0, (k*13+5)%m)
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}
	t100, t10000 := measure(100), measure(10000)
	report("Reach O(1): per-query cost flat from m=100 to m=10000", t10000 < 20*t100)
}

// Compute 后 8 个 goroutine 并发查询同一批 (i,j)，结果逐对一致。
func checkConcurrency() {
	a, _ := api.New(50)
	for i := 0; i < 50; i++ {
		_ = a.AddEdge(i, (i*7+3)%50)
		_ = a.AddEdge(i, (i*13+7)%50)
	}
	a.Compute()
	results := make([][]bool, 8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for k := 0; k < 2500; k++ {
				results[g] = append(results[g], a.Reach((k*7)%50, (k*13)%50))
			}
		}(g)
	}
	close(start)
	wg.Wait()
	ok := true
	for g := 1; g < 8; g++ {
		ok = ok && slices.Equal(results[g], results[0])
	}
	report("concurrent Reach pairwise identical", ok)
}

func main() {
	checkErrors()
	checkClosure()
	checkScaling()
	checkConcurrency()
	report("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
