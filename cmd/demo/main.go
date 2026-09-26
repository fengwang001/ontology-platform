// Command demo 演示弦图判定（MCS + 成团检查）：正确结论、三个陷阱的错值、
// 四类可判定错误、失败不留痕、大 m 性能佐证与并发一致性。退出码 0 表示全过。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ug"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
	}
	s := "OK"
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s %s\n", s, name)
}

func build(n int, edges [][2]int) *api.API {
	a, err := api.New(n)
	if err != nil {
		report("建图", false)
	}
	for _, e := range edges {
		if err := a.AddEdge(e[0], e[1]); err != nil {
			report("加边", false)
		}
	}
	return a
}

func reversed(s []int) []int {
	out := make([]int, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// firstViolatorPairs 按给定顺序做完整的两两成团检查，返回首个违规节点（无则 -1）。
func firstViolatorPairs(g *ug.Graph, order []int) int {
	pos := make([]int, g.N())
	for i, v := range order {
		pos[v] = i
	}
	for _, v := range order {
		var later []int
		for _, w := range g.Neighbors(v) {
			if pos[w] > pos[v] {
				later = append(later, w)
			}
		}
		for i := 0; i < len(later); i++ {
			for j := i + 1; j < len(later); j++ {
				if !g.HasEdge(later[i], later[j]) {
					return v
				}
			}
		}
	}
	return -1
}

// nextOnlyOK 是陷阱(丙)的错误检查：只看序中相邻两项是否相邻。
func nextOnlyOK(g *ug.Graph, order []int) bool {
	for i := 0; i+1 < len(order); i++ {
		if !g.HasEdge(order[i], order[i+1]) {
			return false
		}
	}
	return true
}

func main() {
	edges6 := [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}}
	a := build(6, edges6)
	a.Compute()
	report("六边图 PEO=[5 4 3 2 1 0] 且为弦图",
		reflect.DeepEqual(a.PEO(), []int{5, 4, 3, 2, 1, 0}) && a.IsChordal() && a.FirstViolator() == -1)

	c4 := build(4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}})
	c4.Compute()
	report("C4 非弦图且首个违规节点=3", !c4.IsChordal() && c4.FirstViolator() == 3)

	g6 := ug.New(6)
	for _, e := range edges6 {
		_ = g6.AddEdge(e[0], e[1])
	}
	g4 := ug.New(4)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}} {
		_ = g4.AddEdge(e[0], e[1])
	}

	mcsOnlySaysChordal := true // 陷阱(甲)的错误实现：拿到 MCS 序就直接判弦图
	report("陷阱(甲): 只做MCS不检查会把C4误判为弦图(正确: 非弦图,首违规=3)",
		mcsOnlySaysChordal != c4.IsChordal())
	report("陷阱(乙): 不取逆序会在节点1误判不成团,把弦图误判为非弦图",
		firstViolatorPairs(g6, reversed(a.PEO())) == 1 && a.IsChordal())
	report("陷阱(丙): 只查紧邻下一个会漏检(1,2),把C4误判为弦图",
		nextOnlyOK(g4, c4.PEO()) && !c4.IsChordal())

	b := build(3, [][2]int{{0, 1}})
	_, errN := api.New(-1)
	e1, e2, e3 := b.AddEdge(0, 3), b.AddEdge(2, 2), b.AddEdge(1, 0)
	distinct := errN != e1 && errN != e2 && errN != e3 && e1 != e2 && e2 != e3 && e1 != e3
	report("四类哨兵错误可判定且互不相同",
		errors.Is(errN, api.ErrInvalidN) && errors.Is(e1, api.ErrOutOfRange) &&
			errors.Is(e2, api.ErrSelfLoop) && errors.Is(e3, api.ErrDuplicate) && distinct)
	report("被拒后状态不变且可继续用", b.EdgeCount() == 1 && b.AddEdge(1, 2) == nil && b.EdgeCount() == 2)

	big := build(10000, nil)
	big.Compute()
	report("m=10000 孤立点为弦图(选点检查数不随m增长,由 mcs 包内测试钉住)",
		big.IsChordal() && len(big.PEO()) == 10000)

	var bad atomic.Int32
	var wg sync.WaitGroup
	wantPEO := a.PEO()
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !a.IsChordal() || a.FirstViolator() != -1 || !reflect.DeepEqual(a.PEO(), wantPEO) {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	report("64 个 goroutine 并发读取逐项一致", bad.Load() == 0)
	report("SelfCheck 四条不变量自检通过", a.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
