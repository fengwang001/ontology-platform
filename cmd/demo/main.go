// Command demo 逐项自检并打印 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/poly"
)

var failed bool

func report(ok bool, label string) {
	if ok {
		fmt.Println("OK  " + label)
	} else {
		failed = true
		fmt.Println("FAIL " + label)
	}
}

// 第三节的两个正方形。
var (
	sqA = []poly.Point{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 3, Y: 3}, {X: 0, Y: 3}}
	sqB = []poly.Point{{X: 2, Y: 2}, {X: 5, Y: 2}, {X: 5, Y: 5}, {X: 2, Y: 5}}
)

// checkTenSteps 核验十行分步表：8 个顶点分类 + 2 个边交点。
func checkTenSteps() {
	// A 的顶点对 B：保留/保留/丢弃/保留；B 的顶点对 A：丢弃/保留/保留/保留。
	wantIn := []bool{false, false, true, false, true, false, false, false}
	pts := []poly.Point{sqA[0], sqA[1], sqA[2], sqA[3], sqB[0], sqB[1], sqB[2], sqB[3]}
	ok := true
	for i, p := range pts {
		if poly.PointInPoly(p, [2][]poly.Point{sqB, sqA}[i/4]) != wantIn[i] {
			ok = false
		}
	}
	p1, ok1 := poly.SegIntersect(sqA[1], sqA[2], sqB[0], sqB[1]) // (3,2)
	p2, ok2 := poly.SegIntersect(sqA[2], sqA[3], sqB[3], sqB[0]) // (2,3)
	ok = ok && ok1 && ok2 && p1 == (poly.Point{X: 3, Y: 2}) && p2 == (poly.Point{X: 2, Y: 3})
	report(ok, "ten-step classification + intersections (3,2),(2,3)")
}

// checkUnion 核验正确并集的八顶点序列、面积守恒、结果无自交。
func checkUnion() *api.Polygon {
	A, _ := api.NewPolygon(sqA)
	B, _ := api.NewPolygon(sqB)
	U, err := A.Union(B)
	want := []poly.Point{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 3, Y: 2}, {X: 5, Y: 2},
		{X: 5, Y: 5}, {X: 2, Y: 5}, {X: 2, Y: 3}, {X: 0, Y: 3}}
	got := U.Vertices()
	ok := err == nil && reflect.DeepEqual(got, want)
	report(ok, "union = 8 vertices CCW (0,0)(3,0)(3,2)(5,2)(5,5)(2,5)(2,3)(0,3)")
	report(err == nil && poly.Area2(got)/2 == 9+9-1, "area conservation: 17 == 9+9-1")
	report(err == nil && U.SelfCheck() == nil, "union result is simple (no self-intersection)")
	return U
}

// checkFaults 核验四类可判定错误互不相同、被拒后无部分结果。
func checkFaults() {
	cases := []struct {
		verts []poly.Point
		want  error
	}{
		{[]poly.Point{{X: 0, Y: 0}, {X: 1, Y: 0}}, api.ErrTooFewVertices},
		{[]poly.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 1, Y: 2}, {X: 3, Y: 3}}, api.ErrSelfIntersect},
		{[]poly.Point{{X: 0, Y: 0}, {X: 0, Y: 0}, {X: 1, Y: 1}}, api.ErrDuplicate},
		{[]poly.Point{{X: 0, Y: 0}, {X: 0, Y: 1}, {X: 1, Y: 0}}, api.ErrClockwise},
		{[]poly.Point{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 1}}, api.ErrOutOfRange},
	}
	ok := true
	seen := map[error]bool{}
	for _, c := range cases {
		p, err := api.NewPolygon(c.verts)
		if !errors.Is(err, c.want) || p != nil || seen[c.want] {
			ok = false
		}
		seen[c.want] = true
	}
	report(ok, "four distinct fault errors, no partial result after rejection")
	p, err := api.NewPolygon(sqA) // 被拒后仍可正常使用
	report(err == nil && p != nil, "usable after rejection")
}

// checkLargeM 大 m 下并集可完成；求交次数不随 m² 增长由 TestProbeCountLinear 钉住
// （计数器非导出，demo 不读其数值）。
func checkLargeM() {
	circle := func(cx, cy, r float64, n int) []poly.Point {
		p := make([]poly.Point, n)
		for i := range p {
			a := 2 * math.Pi * float64(i) / float64(n)
			p[i] = poly.Point{X: math.Round(cx + r*math.Cos(a)), Y: math.Round(cy + r*math.Sin(a))}
		}
		return p
	}
	A, _ := api.NewPolygon(circle(0, 0, 9000, 2000))
	B, _ := api.NewPolygon(circle(1000, 0, 9000, 2000))
	_, err := A.Union(B)
	report(err == nil, "large-m union completes; probe count is sub-quadratic (pinned by test)")
}

// checkConcurrent 并发只读同一并集结果，顶点序列逐字段相同（不用 sleep）。
func checkConcurrent(U *api.Polygon) {
	want := U.Vertices()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				if U.SelfCheck() != nil || !reflect.DeepEqual(U.Vertices(), want) {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	report(!bad.Load(), "concurrent read-only Vertices identical across goroutines")
}

func main() {
	checkTenSteps()
	U := checkUnion()
	checkFaults()
	checkLargeM()
	checkConcurrent(U)
	report(api.SelfTest() == nil, "api.SelfTest: four invariants on built-in pairs")
	if failed {
		os.Exit(1)
	}
}
