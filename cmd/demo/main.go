// Command demo 逐项演示凸多边形交集的正确性判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cint"
	"ontology/cvex"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	fails++
}

func sq(x0, y0, x1, y1 float64) []api.Point {
	return []api.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

// same 报告两顶点序列是否完全一致（允许旋转起点；本题交点均为精确二进制分数）。
func same(a, b []api.Point) bool {
	if len(a) != len(b) {
		return false
	}
next:
	for s := range a {
		for i := range a {
			if a[(s+i)%len(a)] != b[i] {
				continue next
			}
		}
		return true
	}
	return len(a) == 0
}

func errOf(v []api.Point) error {
	_, err := api.NewPolygon(v)
	return err
}

func main() {
	// 1. 第三节五对多边形的交集。
	tri := []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}}
	pairs := []struct{ a, b, want []api.Point }{
		{sq(0, 0, 4, 4), sq(2, 2, 6, 6), sq(2, 2, 4, 4)},
		{sq(0, 0, 2, 2), sq(3, 3, 5, 5), nil},
		{tri, sq(1, 1, 5, 5), []api.Point{{X: 1, Y: 1}, {X: 3, Y: 1}, {X: 1, Y: 3}}},
		{sq(0, 0, 4, 2), sq(2, 0, 6, 4), sq(2, 0, 4, 2)},
		{sq(0, 0, 4, 4), sq(4, 0, 8, 4), nil},
	}
	five := true
	for _, pr := range pairs {
		a, _ := api.NewPolygon(pr.a)
		b, _ := api.NewPolygon(pr.b)
		if r, err := a.Intersect(b); err != nil || !same(r.Vertices(), pr.want) {
			five = false
		}
	}
	check("五对交集(P1-P5)", five)
	// 2. 符号写反：B 的边反向（右侧当内部）裁剪 P1 的 A，应被补半平面裁成空。
	wrong := []cvex.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
	bb := []cvex.Point{{X: 2, Y: 2}, {X: 6, Y: 2}, {X: 6, Y: 6}, {X: 2, Y: 6}}
	for i := range bb {
		wrong = cvex.ClipLeft(wrong, bb[(i+1)%len(bb)], bb[i])
	}
	check("符号写反得空集", len(wrong) == 0)
	// 3. 与朴素裁剪一致、凸性自洽、覆盖正确、失败不留痕（api 自检核验四条不变量）。
	check("自检(朴素一致/凸性/覆盖/不留痕)", api.SelfCheck() == nil)
	// 4. 三类可判定错误互不相同。
	e1 := errOf([]api.Point{{X: 0, Y: 0}, {X: 1, Y: 0}})
	e2 := errOf([]api.Point{{X: 0, Y: 0}, {X: 2e4, Y: 0}, {X: 0, Y: 1}})
	e3 := errOf([]api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 1}, {X: 0, Y: 4}})
	check("三类可判定错误", errors.Is(e1, api.ErrTooFewVertices) && errors.Is(e2, api.ErrOutOfRange) &&
		errors.Is(e3, api.ErrNotConvex) && !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))
	// 5. 被拒后无部分结果，且仍可继续正常使用。
	bad, _ := api.NewPolygon([]api.Point{{X: 0, Y: 0}, {X: 1, Y: 0}})
	a5, _ := api.NewPolygon(sq(0, 0, 4, 4))
	b5, _ := api.NewPolygon(sq(2, 2, 6, 6))
	r5, _ := a5.Intersect(b5)
	check("被拒后无部分结果", bad == nil && same(r5.Vertices(), sq(2, 2, 4, 4)))
	// 6. 包围盒预判判空（P2）、共边退化判空（P5）、大 m 不相交全判空。
	var c cint.Clipper
	check("包围盒预判判空(P2)", c.Intersect(sq(0, 0, 2, 2), sq(3, 3, 5, 5)) == nil)
	check("共边退化判空(P5)", c.Intersect(sq(0, 0, 4, 4), sq(4, 0, 8, 4)) == nil)
	empty := true
	for i := 0; i < 10000; i++ {
		if d := float64(100 + i*10); c.Intersect(sq(0, 0, 4, 4), sq(d, d, d+1, d+1)) != nil {
			empty = false
		}
	}
	check("大m不相交全判空(m=10000)", empty)
	// 7. 并发只读同一个交集，结果逐字段相同。
	want := r5.Vertices()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var agree atomic.Bool
	agree.Store(true)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if got := r5.Vertices(); r5.Empty() || !same(got, want) || api.SelfCheck() != nil {
					agree.Store(false)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("并发只读结果一致", agree.Load())
	if fails > 0 {
		os.Exit(1)
	}
}
