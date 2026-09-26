// Command demo runs acceptance checks with no args/network; exit 0
// means every line is OK.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/gh"
	"ontology/match"
)

var failed bool

func ok(c bool, n string) {
	if c {
		fmt.Println("OK  " + n)
	} else {
		fmt.Println("FAIL " + n)
		failed = true
	}
}
func pt(x, y int64) gh.Point { return gh.Point{X: x, Y: y} }

var rm = [4][4]int64{{1, 0, 0, 1}, {0, -1, 1, 0}, {-1, 0, 0, -1}, {0, 1, -1, 0}}

func xf(p gh.Point, r, sc, tx, ty int64) gh.Point { // rotate r*90, scale, translate
	a := rm[r]
	return pt((a[0]*p.X+a[1]*p.Y)*sc+tx, (a[2]*p.X+a[3]*p.Y)*sc+ty)
}

// validSim reports an injective orientation-preserving similarity
// (shared scale, equal signed crosses; mirrors rejected).
func validSim(t, s []gh.Point, m []int) bool {
	if len(m) != len(t) {
		return false
	}
	d := func(a, b gh.Point) int64 { x, y := a.X-b.X, a.Y-b.Y; return x*x + y*y }
	r0, seen := d(t[1], t[0]), map[int]bool{}
	for _, id := range m {
		if id < 0 || id >= len(s) || seen[id] {
			return false
		}
		seen[id] = true
	}
	for i := range t {
		c1 := (t[1].X-t[0].X)*(t[i].Y-t[0].Y) - (t[1].Y-t[0].Y)*(t[i].X-t[0].X)
		c2 := (s[m[1]].X-s[m[0]].X)*(s[m[i]].Y-s[m[0]].Y) - (s[m[1]].Y-s[m[0]].Y)*(s[m[i]].X-s[m[0]].X)
		if (c1 > 0) != (c2 > 0) || (c1 == 0) != (c2 == 0) {
			return false
		}
		for j := i + 1; j < len(t); j++ {
			if d(t[i], t[j])*d(s[m[1]], s[m[0]]) != d(s[m[i]], s[m[j]])*r0 {
				return false
			}
		}
	}
	return true
}
func main() {
	sq := []gh.Point{pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 2)}
	rot := []gh.Point{pt(5, 5), pt(5, 7), pt(3, 7), pt(3, 5)}
	want := []gh.Key{{U: 0, V: 0}, {U: 16, V: 0}, {U: 16, V: 16}, {U: 0, V: 16}}
	keyOK := gh.KeyOf(sq[0], sq[1], sq[0]) == want[0] && gh.KeyOf(sq[0], sq[1], sq[1]) == want[1] && gh.KeyOf(sq[0], sq[1], sq[2]) == want[2] && gh.KeyOf(sq[0], sq[1], sq[3]) == want[3] &&
		gh.KeyOf(rot[0], rot[1], rot[0]) == want[0] && gh.KeyOf(rot[0], rot[1], rot[1]) == want[1] && gh.KeyOf(rot[0], rot[1], rot[2]) == want[2] && gh.KeyOf(rot[0], rot[1], rot[3]) == want[3]
	ok(keyOK, "四点规范坐标")
	_ = api.NewTemplate(sq)
	m, f, _ := api.Match(rot)
	ok(f && m[0] == 0 && m[3] == 3, "旋转90°场景匹配")
	// 与穷举一致：非对称四边形副本+噪声必中且映射合法；共线场景必不中。
	tpl := []gh.Point{pt(0, 0), pt(5, 0), pt(1, 3), pt(4, 1)}
	_ = api.NewTemplate(tpl)
	noisy := []gh.Point{pt(-30, -30), pt(30, 27)}
	for _, p := range tpl {
		noisy = append(noisy, xf(p, 1, 2, 7, 3))
	}
	collinear := []gh.Point{pt(-3, 0), pt(-2, 0), pt(-1, 0), pt(0, 0), pt(1, 0), pt(2, 0), pt(3, 0)}
	mA, fA, _ := api.Match(noisy)
	_, fB, eB := api.Match(collinear)
	ok(fA && validSim(tpl, noisy, mA) && !fB && eB == nil, "与穷举一致")
	// 相似不变：对模板与场景再施加同一 R270+平移，映射逐位相等。
	_ = api.NewTemplate(sq)
	s1 := []gh.Point{}
	for _, p := range sq {
		s1 = append(s1, xf(p, 1, 2, 0, 0))
	}
	m1, f1, _ := api.Match(s1)
	g := func(p gh.Point) gh.Point { return xf(p, 3, 1, 20, 20) }
	t2, s2 := []gh.Point{}, []gh.Point{}
	for i := range sq {
		t2, s2 = append(t2, g(sq[i])), append(s2, g(s1[i]))
	}
	_ = api.NewTemplate(t2)
	m2, f2, _ := api.Match(s2)
	sim := f1 && f2 && len(m1) == len(m2)
	for i := range m1 {
		sim = sim && m1[i] == m2[i]
	}
	ok(sim, "相似不变")
	_ = api.NewTemplate([]gh.Point{pt(0, 0), pt(4, 0), pt(1, 3)})
	_, mf, _ := api.Match([]gh.Point{pt(5, 5), pt(9, 5), pt(6, 2)})
	ok(!mf && api.SelfCheck() == nil, "镜像副本判不匹配")
	distinct := map[error]bool{}
	for _, e := range []error{api.ErrTooFewPoints, api.ErrCollinear, api.ErrSceneTooSmall, api.ErrDuplicatePoint, api.ErrOutOfRange} {
		distinct[e] = true
	}
	badPts := [][]gh.Point{{pt(0, 0), pt(1, 1)}, {pt(0, 0), pt(1, 1), pt(2, 2)}, {pt(0, 0), pt(1, 0), pt(0, 1), pt(0, 0)}, {pt(0, 0), pt(1, 0), pt(0, 10001)}}
	errOK := len(distinct) == 5
	for _, b := range badPts {
		errOK = errOK && api.NewTemplate(b) != nil
	}
	ok(errOK, "五类可判定错误")
	_ = api.NewTemplate(sq)
	_, _, eSmall := api.Match([]gh.Point{pt(0, 0)})
	mm, still, _ := api.Match(rot)
	ok(errors.Is(eSmall, api.ErrSceneTooSmall) && still && mm[2] == 2, "被拒后无部分结果")
	quad := match.VoteBound(4, 100) <= 2*100*100 && match.VoteBound(4, 1000) <= 2*1000*1000 && match.VoteBound(4, 10000) <= 2*10000*10000
	ok(quad, "大n投票次数随n²增长(预算≤2n²)")
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ref []int
	same := true
	for gN := 0; gN < 8; gN++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr, ff, _ := api.Match(rot)
			mu.Lock()
			defer mu.Unlock()
			if !ff {
				same = false
			} else if ref == nil {
				ref = append([]int(nil), rr...)
			} else {
				for i := range rr {
					same = same && rr[i] == ref[i]
				}
			}
		}()
	}
	wg.Wait()
	ok(same, "并发只读结果一致")
	if failed {
		panic("demo failed")
	}
}
