// Command demo 逐条校验投影下推与列裁剪的关键结论，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/col"
	"ontology/prune"
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

func eqS(a, b []string) bool {
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

func eqI(a, b []int) bool {
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

func main() {
	s, _ := col.New([]string{"a", "b", "c", "d", "e"})
	defs := []prune.Def{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}
	// 六步之 1-3：每多定义一个输出列，保留集/裁剪集随之扩大。
	wantKeep := [][]string{{"a"}, {"a", "b", "c"}, {"a", "b", "c", "d"}}
	okSteps := true
	for i := range defs {
		p, err := prune.New(s, defs[:i+1])
		if err != nil || !eqS(p.Retained(), wantKeep[i]) {
			okSteps = false
		}
	}
	check("六步1-3 保留集 {a}->{a,b,c}->{a,b,c,d}，裁剪集相应缩至 {e}", okSteps)

	// 六步之 4-6：三行投影（别名、聚合、逐行现算都在此体现）。
	p, _ := prune.New(s, defs)
	rows := []map[string]int{
		{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
		{"a": 6, "b": 7, "c": 8, "d": 9, "e": 10},
		{"a": 11, "b": 12, "c": 13, "d": 14, "e": 15},
	}
	g := make([][]int, 3)
	for i := range rows {
		g[i] = p.Project(rows[i])
	}
	check("六步4-6 三行投影 (1,5,4)(6,15,9)(11,25,14)",
		eqI(g[0], []int{1, 5, 4}) && eqI(g[1], []int{6, 15, 9}) && eqI(g[2], []int{11, 25, 14}))
	check("别名 x 取源列 a=1（按输出名读会错成0）", g[0][0] == 1)
	check("聚合依赖 b,c 未被裁，首行 sum=5（误裁会错成0）", g[0][1] == 5)
	check("聚合列逐行现算，次行 sum=15（首算缓存会错成5）", g[1][1] == 15)

	// 四类哨兵互不相同；被拒操作不留痕。
	e, _ := api.New([]string{"a", "b"})
	_ = e.SetProjection([]api.Output{{Out: "x", Refs: []string{"a"}}})
	before := len(e.View())
	_, errSchema := api.New([]string{"a", ""})
	errRef := e.SetProjection([]api.Output{{Out: "z", Refs: []string{"nope"}}})
	errDup := e.SetProjection([]api.Output{{Out: "z", Refs: []string{"a"}}, {Out: "z", Refs: []string{"b"}}})
	errRow := e.Apply(map[string]int{"a": 1})
	sentinels := []error{api.ErrInvalidSchema, api.ErrUnknownRef, api.ErrDuplicateOutput, api.ErrInvalidRow}
	distinct, hits := true, errors.Is(errSchema, sentinels[0]) && errors.Is(errRef, sentinels[1]) &&
		errors.Is(errDup, sentinels[2]) && errors.Is(errRow, sentinels[3])
	for i := range sentinels {
		for j := i + 1; len(sentinels) > j; j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				distinct = false
			}
		}
	}
	check("四类哨兵错误互不相同且各自命中", distinct && hits)
	check("被拒操作不留痕：投影仍 x、视图长度不变", eqS(e.OutputNames(), []string{"x"}) && len(e.View()) == before)

	// 宽表多档：只引用 2 列，保留集恒定为 2，不随 m 增长。
	widthOK := true
	for _, m := range []int{100, 1000, 10000} {
		names, row := make([]string, m), map[string]int{}
		for i := range names {
			names[i] = fmt.Sprintf("c%d", i)
			row[names[i]] = i
		}
		ws, _ := col.New(names)
		wp, err := prune.New(ws, []prune.Def{{Out: "o1", Refs: []string{"c0"}}, {Out: "o2", Refs: []string{"c42"}}})
		if err != nil || len(wp.Retained()) != 2 || !eqI(wp.Project(row), []int{0, 42}) {
			widthOK = false
		}
	}
	check("宽表 m=100/1000/10000 只保留并读取2列", widthOK)

	// 并发只读一致（无 sleep）。
	_ = e.Apply(map[string]int{"a": 3, "b": 4})
	want := e.View()
	var wg sync.WaitGroup
	raceOK := true
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := e.View()
			if len(v) != len(want) {
				raceOK = false
				return
			}
			for i := range v {
				if !eqI(v[i], want[i]) {
					raceOK = false
				}
			}
		}()
	}
	wg.Wait()
	check("16 goroutine 并发只读 View 逐行逐列一致", raceOK)
	check("SelfCheck 四条不变量", e.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
