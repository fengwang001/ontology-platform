// 演示程序：不读参数、不联网。逐条打印几何哈希判定，全 OK 退出码 0。
package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sync"

	"ontology/api"
	"ontology/gh"
	"ontology/match"
)

var allOK = true

func report(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag, allOK = "FAIL", false
	}
	fmt.Printf("%s %s\n", tag, name)
}
func P(x ...int) []gh.Point {
	o := make([]gh.Point, 0, len(x)/2)
	for i := 0; i+1 < len(x); i += 2 {
		o = append(o, gh.Point{X: x[i], Y: x[i+1]})
	}
	return o
}
func eq(a, b []int) bool {
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
	sq := P(0, 0, 2, 0, 2, 2, 0, 2)
	want := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} // 第三节四行表
	okCoord := true
	for i, w := range want {
		u, v := gh.BasisUV(sq[0], sq[1], sq[i])
		if math.Abs(u-w[0]) > 1e-12 || math.Abs(v-w[1]) > 1e-12 {
			okCoord = false
		}
	}
	report("canonical coordinates (4 rows)", okCoord)

	es, _ := api.NewTemplate(sq)
	m1, f1, _ := es.Match(P(5, 5, 5, 7, 3, 7, 3, 5)) // 旋90°+(5,5)
	report("rot90 scene matches", f1 && eq(m1, []int{0, 1, 2, 3}))
	report("agrees with exhaustive search", exhaustiveAgree())
	m2, f2, _ := es.Match(gh.Xform(sq, 2, 0, -30, -40)) // 缩放2+平移
	report("similarity invariant mapping", f2 && eq(m1, m2))

	tr := P(0, 0, 4, 0, 1, 3) // 非对称三角形验无反射
	et, _ := api.NewTemplate(tr)
	_, f5, _ := et.Match(P(9, 9, 13, 9, 10, 6))
	report("mirror copy rejected", !f5)
	report("five distinct sentinel errors", fiveErrors())
	_, _, _ = es.Match(P(0, 0, 1, 0)) // 触发拒绝
	_, f7, _ := es.Match(sq)
	report("no partial result after rejection", f7)
	report("vote count grows as n^2", match.QuadraticVoteCountVerified())
	report("concurrent reads identical", concurrentIdentical(et, tr))

	if !allOK {
		os.Exit(1)
	}
}

// exhaustiveAgree：一半场景埋真实保向相似副本+噪声，哈希结论须与穷举一致。
func exhaustiveAgree() bool {
	rg := rand.New(rand.NewPCG(4242, 776))
	tpl := P(0, 0, 5, 0, 5, 4)
	e, _ := api.NewTemplate(tpl)
	mats := [][2]int{{1, 0}, {0, 1}, {2, 1}, {1, 2}}
	for it := 0; it < 24; it++ {
		sce, seen := []gh.Point{}, map[uint64]bool{}
		if it%2 == 0 {
			c := mats[rg.Int64()%int64(len(mats))]
			sce = gh.Xform(tpl, c[0], c[1], 200, 200)
			for _, q := range sce {
				seen[gh.Key(q)] = true
			}
		}
		for len(sce) < 8 {
			p := gh.Point{X: 200 + int(rg.Int64())%200, Y: 200 + int(rg.Int64())%200}
			if !seen[gh.Key(p)] {
				seen[gh.Key(p)], sce = true, append(sce, p)
			}
		}
		if _, f, err := e.Match(sce); err != nil || f != gh.BruteForceContains(tpl, sce) {
			return false
		}
	}
	return true
}

// fiveErrors 核验五类哨兵错误互不相同且各自可判定。
func fiveErrors() bool {
	e, _ := api.NewTemplate(P(0, 0, 2, 0, 2, 2, 0, 2))
	got := map[error]bool{}
	for _, c := range []struct {
		call func() error
		want error
	}{
		{func() error { _, e := api.NewTemplate(P(0, 0, 1, 0)); return e }, api.ErrTooFewTemplatePoints},
		{func() error { _, e := api.NewTemplate(P(0, 0, 1, 0, 2, 0)); return e }, api.ErrCollinearTemplate},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0)); return e }, api.ErrSceneTooSmall},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 0, 0)); return e }, api.ErrDuplicatePoint},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 0, 10001)); return e }, api.ErrCoordinateOutOfRange},
	} {
		if c.call() == c.want {
			got[c.want] = true
		}
	}
	return len(got) == 5
}

// concurrentIdentical：N 个 goroutine 只读同一模板/场景，结果逐字段一致。
func concurrentIdentical(e *api.Engine, tpl []gh.Point) bool {
	sce := append(gh.Xform(tpl, 2, 1, 300, 300),
		gh.Point{X: 0, Y: 0}, gh.Point{X: 1, Y: 9}, gh.Point{X: 9, Y: 1})
	const N = 16
	var wg sync.WaitGroup
	found := make([]bool, N)
	maps := make([][]int, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); maps[g], found[g], _ = e.Match(sce) }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if found[g] != found[0] || !eq(maps[g], maps[0]) {
			return false
		}
	}
	return found[0]
}
