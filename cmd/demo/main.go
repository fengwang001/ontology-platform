// demo：平面嵌入面追踪与欧拉校验的逐项判定演示。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/pg"
)

var fails int

func report(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

// trapFace 用"取后继"的错误 next 从 u→v 出发走一个面。
func trapFace(g *pg.Graph, u, v int) []int {
	var walk []int
	a, b := u, v
	for {
		walk = append(walk, a)
		rot := g.Rotation(b)
		i := 0
		for rot[i] != a {
			i++
		}
		a, b = b, rot[(i+1)%len(rot)] // 错：后面而非前面
		if a == u && b == v {
			break
		}
	}
	return walk
}

func main() {
	edges := [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}}
	rots := [][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}}
	a, _ := api.New(4)
	g, _ := pg.New(4)
	for _, e := range edges {
		_ = a.AddEdge(e[0], e[1])
		_ = g.AddEdge(e[0], e[1])
	}
	for v, r := range rots {
		_ = a.SetRotation(v, r)
		_ = g.SetRotation(v, r)
	}
	a.Compute()
	report("第三节嵌入", a.FaceCount() == 3 && a.EulerHolds(),
		fmt.Sprintf("faces=%v F=%d V-E+F=%d=1+C", a.Faces(), a.FaceCount(), 4-5+a.FaceCount()))
	trap := trapFace(g, 0, 1)
	report("(甲)next方向反", fmt.Sprint(trap) == "[0 1 3]",
		fmt.Sprintf("错面=%v 正确=[0 1 2]", trap))
	report("(乙)连通假设陷阱", 6-6+3 != 2 && 6-6+3 == 1+2,
		"误用V-E+F=2: 3!=2误判不成立; 正确3=1+C(C=2)成立")
	report("(丙)漏外平面陷阱", 4-5+2 != 2 && 4-5+3 == 2,
		"F=2: 1!=2误判不成立; 正确F=3: 2=1+1成立")
	e1 := a.AddEdge(0, 9)
	e2 := a.AddEdge(1, 1)
	e3 := a.AddEdge(0, 1)
	e4 := a.SetRotation(0, []int{2, 3})
	_, e0 := api.New(0)
	report("四类可判定错误", errors.Is(e0, pg.ErrNonPositiveN) &&
		errors.Is(e1, pg.ErrNodeOutOfRange) && errors.Is(e2, pg.ErrSelfLoop) &&
		errors.Is(e3, pg.ErrDuplicateEdge) && errors.Is(e4, pg.ErrBadRotation),
		fmt.Sprintf("%v|%v|%v|%v|%v", e0, e1, e2, e3, e4))
	report("被拒后状态不变", a.EdgeCount() == 5 && a.FaceCount() == 3,
		fmt.Sprintf("E=%d F=%d", a.EdgeCount(), a.FaceCount()))
	star, _ := api.New(10001)
	for i := 1; i <= 10000; i++ {
		_ = star.AddEdge(0, i)
	}
	star.Compute()
	report("大度数星图m=10000", star.FaceCount() == 1 && star.EulerHolds(),
		"F=1 欧拉成立; 定位检查数不随m增长见face包内测试")
	var wg sync.WaitGroup
	res := make(chan bool, 160)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				res <- a.FaceCount() == 3 && a.EulerHolds()
			}
		}()
	}
	wg.Wait()
	close(res)
	same := true
	for r := range res {
		same = same && r
	}
	report("并发读取一致", same, "16 goroutine x 10 次结果全同")
	report("SelfCheck", api.SelfCheck(), "四条不变量内置核验")
	if fails > 0 {
		os.Exit(1)
	}
}
