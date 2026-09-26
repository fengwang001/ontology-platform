// Package api 是几何哈希对外入口：构造模板、场景匹配、自检。只依赖 match 与 gh。
package api

import (
	"errors"
	"math"
	"math/rand/v2"

	"ontology/gh"
	"ontology/match"
)

// 五类可判定哨兵错误，互不相同；拒绝时不产生任何部分结果。
var ErrTooFewTemplatePoints = errors.New("template must contain at least 3 points")
var ErrCollinearTemplate = errors.New("template points must not be all collinear")
var ErrSceneTooSmall = errors.New("scene has fewer points than template")
var ErrDuplicatePoint = errors.New("point set contains duplicate points")
var ErrCoordinateOutOfRange = errors.New("coordinate exceeds |X|,|Y| <= 10000")

const coordLimit = 10000

// Engine 持有不可变模板；Match 全只读，可被多 goroutine 并发调用。
type Engine struct{ t *match.Template }

// NewTemplate 全部校验通过后才构造；任一失败整体拒绝、不留部分结果。
func NewTemplate(pts []gh.Point) (*Engine, error) {
	if len(pts) < 3 {
		return nil, ErrTooFewTemplatePoints
	}
	if err := checkPoints(pts); err != nil {
		return nil, err
	}
	if gh.Collinear(pts) {
		return nil, ErrCollinearTemplate
	}
	return &Engine{t: match.NewTemplate(append([]gh.Point(nil), pts...))}, nil
}

// Match 校验场景后只读投票；不匹配 mapping=nil，Engine 状态始终不变。
func (e *Engine) Match(scene []gh.Point) (mapping []int, found bool, err error) {
	if len(scene) < e.t.K() {
		return nil, false, ErrSceneTooSmall
	}
	if err := checkPoints(scene); err != nil {
		return nil, false, err
	}
	r := e.t.Match(scene)
	if !r.Found {
		return nil, false, nil
	}
	return append([]int(nil), r.Mapping...), true, nil
}
func checkPoints(pts []gh.Point) error { // 先查越界再查重（两类错误互异）
	seen := map[uint64]bool{}
	for _, p := range pts {
		if p.X > coordLimit || -p.X > coordLimit || p.Y > coordLimit || -p.Y > coordLimit {
			return ErrCoordinateOutOfRange
		}
		if seen[gh.Key(p)] {
			return ErrDuplicatePoint
		}
		seen[gh.Key(p)] = true
	}
	return nil
}
func P(x ...int) []gh.Point { // P(x0,y0,x1,y1,...) 快速建点集
	o := make([]gh.Point, 0, len(x)/2)
	for i := 0; i+1 < len(x); i += 2 {
		o = append(o, gh.Point{X: x[i], Y: x[i+1]})
	}
	return o
}

// SelfCheck 用内置模板/场景对核验四条不变量；任一不成立返回错误。只读、可并发。
func SelfCheck() error {
	sq := P(0, 0, 2, 0, 2, 2, 0, 2)
	want := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} // 第三节四行表
	for i, w := range want {
		if u, v := gh.BasisUV(sq[0], sq[1], sq[i]); math.Abs(u-w[0]) > 1e-12 || math.Abs(v-w[1]) > 1e-12 {
			return errors.New("selfcheck: canonical coordinates")
		}
	}
	e, err := NewTemplate(sq)
	if err != nil {
		return err
	}
	if _, f, err := e.Match(P(5, 5, 5, 7, 3, 7, 3, 5)); err != nil || !f {
		return errors.New("selfcheck: rot90 match") // 旋90°+(5,5)
	}
	if _, f, err := e.Match(gh.Xform(sq, 2, 0, -30, -40)); err != nil || !f {
		return errors.New("selfcheck: similarity invariant") // 缩放2+平移
	}
	tr := P(0, 0, 4, 0, 1, 3) // 正方形镜像可旋转重合，改用非对称三角形验无反射
	te, err := NewTemplate(tr)
	if err != nil {
		return err
	}
	if _, f, err := te.Match(P(9, 9, 13, 9, 10, 6)); err != nil || f || // 反射(x,-y)+(9,9)
		gh.BruteForceContains(tr, P(9, 9, 13, 9, 10, 6)) {
		return errors.New("selfcheck: mirror must not match")
	}
	if err := rejectSelfCheck(e); err != nil {
		return err
	}
	return exhaustiveSelfCheck(te, tr)
}
func rejectSelfCheck(e *Engine) error { // 五类互异哨兵 + 拒绝后仍可用
	for _, c := range []struct {
		call func() error
		err  error
	}{
		{func() error { _, e := NewTemplate(P(0, 0, 1, 0)); return e }, ErrTooFewTemplatePoints},
		{func() error { _, e := NewTemplate(P(0, 0, 1, 0, 2, 0)); return e }, ErrCollinearTemplate},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0)); return e }, ErrSceneTooSmall},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 0, 0)); return e }, ErrDuplicatePoint},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 0, 10001)); return e }, ErrCoordinateOutOfRange},
	} {
		if err := c.call(); err != c.err {
			return errors.New("selfcheck: rejection " + c.err.Error())
		}
	}
	if _, f, err := e.Match(P(0, 0, 2, 0, 2, 2, 0, 2)); err != nil || !f {
		return errors.New("selfcheck: unusable after rejection")
	}
	return nil
}
func exhaustiveSelfCheck(e *Engine, tpl []gh.Point) error { // 哈希结论须与穷举一致
	rg := rand.New(rand.NewPCG(20260926, 776))
	mats := [][2]int{{1, 0}, {0, 1}, {2, 1}, {1, 2}, {-1, 2}}
	for iter := 0; iter < 30; iter++ {
		sce, seen := []gh.Point{}, map[uint64]bool{}
		if iter%2 == 0 { // 一半埋真实保向相似副本
			c := mats[rg.Int64()%int64(len(mats))]
			sce = gh.Xform(tpl, c[0], c[1], 100+int(rg.Int64())%50, 100+int(rg.Int64())%50)
			for _, q := range sce {
				seen[gh.Key(q)] = true
			}
		}
		for len(sce) < 9 { // 其余为去重噪声点
			p := gh.Point{X: int(rg.Int64()) % 300, Y: int(rg.Int64()) % 300}
			if !seen[gh.Key(p)] {
				seen[gh.Key(p)], sce = true, append(sce, p)
			}
		}
		if _, f, err := e.Match(sce); err != nil || f != gh.BruteForceContains(tpl, sce) {
			return errors.New("selfcheck: exhaustive mismatch")
		}
	}
	return nil
}
