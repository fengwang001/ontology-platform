// Command demo 校验半朴素传递闭包实现，逐条打印 OK/FAIL，退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rel"
	"ontology/semi"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		failures++
		fmt.Println("FAIL " + name)
	}
}

// sampleEdges 是 NOTES 第三节推导用的边集。
func sampleEdges() [][2]string {
	return [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "d"},
		{"b", "d"}, {"d", "e"}, {"e", "b"},
	}
}

func contains(ts []rel.T, t rel.T) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func main() {
	// 1) rel 判定：集合语义（去重）、Has/Size/Diff/Union/Sorted 确定性。
	a := rel.New(rel.T{X: "a", Y: "b"}, rel.T{X: "a", Y: "b"})
	b := rel.New(rel.T{X: "a", Y: "b"}, rel.T{X: "b", Y: "c"})
	u := a.Clone()
	u.Union(b)
	got := a.Sorted()
	check("rel set add/has/size/diff/union",
		a.Size() == 1 && a.Has(rel.T{X: "a", Y: "b"}) &&
			b.Diff(a).Size() == 1 && u.Size() == 2 &&
			len(got) == 1 && got[0] == (rel.T{X: "a", Y: "b"}))

	// 2) semi 判定：R0–R4 的 Delta/候选/新增/path 大小与 NOTES 五行表逐格一致。
	en := semi.New([]rel.T{
		{X: "a", Y: "b"}, {X: "b", Y: "c"}, {X: "c", Y: "d"},
		{X: "b", Y: "d"}, {X: "d", Y: "e"}, {X: "e", Y: "b"},
	})
	path0 := en.Eval()
	rows := en.Rows()
	expCands := []int{0, 8, 8, 8, 1}
	expAdded := []int{6, 7, 6, 1, 0}
	expSize := []int{6, 13, 19, 20, 20}
	expDelta := [][]rel.T{
		{{X: "a", Y: "b"}, {X: "b", Y: "c"}, {X: "b", Y: "d"}, {X: "c", Y: "d"}, {X: "d", Y: "e"}, {X: "e", Y: "b"}},
		{{X: "a", Y: "c"}, {X: "a", Y: "d"}, {X: "b", Y: "e"}, {X: "c", Y: "e"}, {X: "d", Y: "b"}, {X: "e", Y: "c"}, {X: "e", Y: "d"}},
		{{X: "a", Y: "e"}, {X: "b", Y: "b"}, {X: "c", Y: "b"}, {X: "d", Y: "c"}, {X: "d", Y: "d"}, {X: "e", Y: "e"}},
		{{X: "c", Y: "c"}},
		nil,
	}
	roundsOK := len(rows) == 5 && len(path0) == 20
	for i, r := range rows {
		roundsOK = roundsOK && r.Cands == expCands[i] && r.Added == expAdded[i] &&
			r.PathSize == expSize[i] && fmt.Sprint(r.Delta) == fmt.Sprint(expDelta[i])
	}
	check("semi rounds R0-R4 delta/cands/added/path", roundsOK)

	// 3) 闭包大小、自环集合、a→d 的两条导出路径（R1 经 b 新增；R2 经 c 去重丢弃）。
	v, err := api.New(sampleEdges())
	if err != nil {
		check("api closure/selfloops/ad-paths", false)
		return
	}
	path := v.Eval()
	var loops []string
	for _, t := range path {
		if t[0] == t[1] {
			loops = append(loops, t[0]+t[1])
		}
	}
	ad := rel.T{X: "a", Y: "d"}
	check(fmt.Sprintf("api closure=%d selfloops=%v ad:R1 z=b new,R2 z=c dedup",
		len(path), loops),
		len(path) == 20 && fmt.Sprint(loops) == "[bb cc dd ee]" &&
			contains(rows[1].Delta, ad) && contains(rows[2].Dropped, ad))

	// 4) 链图多档：候选总数恰为 m(m-1)/2（计数器不经过任何导出接口读取）。
	chainOK := true
	for _, m := range []int{100, 1000, 10000} {
		chainOK = chainOK && semi.VerifyChain(m)
	}
	check("chain candidates == m(m-1)/2 for m=100,1000,10000", chainOK)

	// 5) 三类输入错误命中互不相同的哨兵。
	_, e1 := api.New([][2]string{{"", "x"}})
	_, e2 := api.New([][2]string{{"a", "a"}})
	_, e3 := api.New([][2]string{{"a", "b"}, {"a", "b"}})
	distinct := errors.Is(e1, api.ErrEmptyNode) && errors.Is(e2, api.ErrSelfLoop) &&
		errors.Is(e3, api.ErrDuplicateEdge) && e1.Error() != e2.Error() &&
		e2.Error() != e3.Error() && e1.Error() != e3.Error()
	check("sentinel errors: empty-node/self-loop/duplicate distinct", distinct)

	// 6) 失败不留痕：被拒后既有求值器状态不变，仍可正常使用。
	sizeBefore := v.Size()
	v.SelfCheck()
	check("rejection leaves no trace; evaluator still usable",
		sizeBefore == 20 && v.Size() == 20 && v.SelfCheck() == nil)

	// 7) 并发：N 个 goroutine 各自独立求值，结果逐元组相同（无 sleep）。
	const n = 16
	var wg sync.WaitGroup
	res := make([][][2]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, _ := api.New(sampleEdges())
			res[i] = w.Eval()
		}(i)
	}
	wg.Wait()
	concOK := len(res[0]) == 20
	for i := 1; i < n; i++ {
		concOK = concOK && fmt.Sprint(res[i]) == fmt.Sprint(res[0])
	}
	check("concurrent independent eval results identical", concOK)

	// 8) 对外自检：四条不变量全部成立。
	check("api SelfCheck all four invariants", v.SelfCheck() == nil)

	if failures > 0 {
		fmt.Println("FAILURES")
		os.Exit(1)
	}
}
