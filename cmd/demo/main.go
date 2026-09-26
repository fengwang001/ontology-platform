// 演示程序：不读参数、不联网；逐条打印平面嵌入面追踪各判定的 OK/FAIL。
package main

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/face"
	"ontology/pg"
)

var (
	edges = [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}}
	rots  = [][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}}
)

func build(n int, es [][2]int, rs [][]int) *api.API {
	a, _ := api.New(n)
	for _, e := range es {
		a.AddEdge(e[0], e[1])
	}
	for v, r := range rs {
		a.SetRotation(v, r)
	}
	if err := a.Compute(); err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
	return a
}

// succTrace 模拟(甲)的错误规则：取环序中紧挨 u「后面」的邻居。
func succTrace(rs [][]int, u, v int) []int {
	seq, su, sv := []int{u}, u, v
	for {
		seq = append(seq, sv)
		r := rs[sv]
		i := slices.Index(r, su)
		su, sv = sv, r[(i+1)%len(r)]
		if su == u && sv == v {
			break
		}
	}
	return seq[:len(seq)-1]
}

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
		return
	}
	fmt.Println("FAIL " + name)
	os.Exit(1)
}

func main() {
	// 1) 第三节嵌入：全部面、F=3、欧拉成立。
	a := build(4, edges, rots)
	correctFace := []int{0, 1, 2}
	report(fmt.Sprintf("faces=%v F=%d euler=%v", a.Faces(), a.FaceCount(), a.EulerHolds()),
		a.FaceCount() == 3 && a.EulerHolds() && slices.Equal(a.Faces()[2], correctFace))

	// 2) (甲) next 取后继的陷阱：0→1 错走出 [0 1 3]，正确应为 [0 1 2]。
	wrong := succTrace(rots, 0, 1)
	report(fmt.Sprintf("trap-next succ-walk=%v (correct %v)", wrong, correctFace),
		slices.Equal(wrong, []int{0, 1, 3}))

	// 3) (乙) 连通假设陷阱：双三角形 V=6,E=6,F=3,C=2，硬套 =2 得 3≠2 被误拒。
	d := build(6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {3, 4}, {4, 5}, {3, 5}},
		[][]int{{1, 2}, {0, 2}, {1, 0}, {4, 5}, {3, 5}, {4, 3}})
	lhs := 6 - d.EdgeCount() + d.FaceCount()
	report(fmt.Sprintf("trap-connected 6-6+%d=%d!=2 false-reject (correct 1+2=3)", d.FaceCount(), lhs),
		d.EulerHolds() && lhs == 3 && lhs != 2)

	// 4) (丙) 漏外平面陷阱：F'=2 时 4-5+2=1≠2 被误拒；正确 F=3 得 2。
	fIn := a.FaceCount() - 1
	report(fmt.Sprintf("trap-outer F'=%d 4-5+%d=%d!=2 false-reject (correct F=3)", fIn, fIn, 4-5+fIn),
		fIn == 2 && (4-5+fIn) != 2 && a.EulerHolds())

	// 5) 四类可判定错误互不相同。
	_, e0 := api.New(0)
	x := build(4, [][2]int{{0, 1}}, [][]int{{1}, {0}, nil, nil})
	errs := []error{e0, x.AddEdge(0, 4), x.AddEdge(1, 1), x.AddEdge(0, 1), x.SetRotation(0, []int{2})}
	want := []error{api.ErrBadN, api.ErrBadVertex, api.ErrSelfLoop, api.ErrDuplicate, api.ErrBadRotation}
	distinct := true
	for i := range errs {
		if errs[i] != want[i] {
			distinct = false
		}
	}
	report("errors badN/badVertex/selfLoop/dup/badRotation distinct", distinct)

	// 6) 拒绝后状态不变且仍可继续使用。
	unusable := false
	if x.EdgeCount() != 1 {
		unusable = true
	}
	x.AddEdge(2, 3)
	for v, r := range [][]int{{1}, {0}, {3}, {2}} {
		x.SetRotation(v, r)
	}
	if err := x.Compute(); err != nil || !x.EulerHolds() {
		unusable = true
	}
	report("state unchanged after rejects, still usable (E=1, then euler)", !unusable)

	// 7) 中心节点大度数 m=10000：前驱定位检查邻居数恒 ≤2（O(1)，不读计数值）。
	const m = 10000
	g, _ := pg.New(m + 1)
	ord := make([]int, m)
	for i := 1; i <= m; i++ {
		g.AddEdge(0, i)
		ord[i-1] = i
		g.SetRotation(i, []int{0})
	}
	g.SetRotation(0, ord)
	tr, err := face.New(g.Snapshot())
	report(fmt.Sprintf("prev-probe O(1) at degree m=%d", m), err == nil && tr.PrevProbeConstant())

	// 8) Compute 完成后并发读取结果一致（不使用 sleep）。
	var wg sync.WaitGroup
	start := make(chan struct{})
	bad := 0
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a.FaceCount() != 3 || !a.EulerHolds() {
				bad++
			}
		}()
	}
	close(start)
	wg.Wait()
	report("32 goroutines concurrent reads consistent", bad == 0)

	// 9) 内置自检（四条不变量）。
	s, _ := api.New(1)
	report("SelfCheck", s.SelfCheck() == nil)
}
