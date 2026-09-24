// Package api 是 GROUPING SETS 增量聚合视图入口；依赖方向单向：api → agg → gset。
package api

import (
	"errors"
	"reflect"

	"ontology/agg"
	"ontology/gset"
)

// Fact 是上游三维事实：M>0 累加，M<0 撤回。
type Fact = agg.Fact

// 四个互不相同的哨兵错误，可用 errors.Is 判定。
var (
	ErrInvalidGroupingSets = errors.New("api: invalid grouping sets") // 分组集为空/重复子集/下标越界
	ErrEmptyDimension      = agg.ErrEmptyDimension                    // A/B/C 任一为空串
	ErrZeroMeasure         = agg.ErrZeroMeasure                       // M == 0
	ErrNegativeResult      = agg.ErrNegativeResult                    // 撤回会使某 (组,键) 变负
)

// GroupingView 是指定分组集上的求和物化视图。
type GroupingView struct{ eng *agg.Engine }

// New 用维度下标列表创建视图：每个 []int 是一个分组集。
// 列表不得为空；同一子集（书写顺序无关）不得重复；下标必须在 [0,3)。
func New(groups [][]int) (*GroupingView, error) {
	if len(groups) == 0 {
		return nil, ErrInvalidGroupingSets
	}
	gs := make([]gset.Group, 0, len(groups))
	seen := map[int]bool{}
	for _, dims := range groups {
		g, ok := gset.Parse(dims) // 越界 / 组内重复维度在此被拒。
		if !ok || seen[g.ID()] {  // 同 ID 即同一子集。
			return nil, ErrInvalidGroupingSets
		}
		seen[g.ID()] = true
		gs = append(gs, g)
	}
	return &GroupingView{eng: agg.New(gs)}, nil
}

// Apply 增量施加一条事实；被拒时返回哨兵错误且视图整体不变。
func (v *GroupingView) Apply(f Fact) error { return v.eng.Apply(f) }

// View 返回组 ID → 投影键串 → sum 的深拷贝。
func (v *GroupingView) View() map[int]map[string]int64 { return v.eng.Snapshot() }

var builtIn = []Fact{
	{A: "x", B: "p", C: "u", M: 10}, {A: "x", B: "p", C: "v", M: 20},
	{A: "x", B: "q", C: "u", M: 5}, {A: "y", B: "p", C: "u", M: 7},
	{A: "x", B: "p", C: "v", M: -20}, {A: "y", B: "p", C: "u", M: 3},
	{A: "x", B: "p", C: "u", M: -10}, {A: "x", B: "q", C: "u", M: 2},
}

// SelfCheck 用第三节的内置八事实序列独立核验第二节的四条不变量，全成立返回 nil。
func (v *GroupingView) SelfCheck() error {
	gv, err := New([][]int{{0}, {0, 1}, {}}) // G={(A),(A,B),()}
	if err != nil {
		return err
	}
	gs := []gset.Group{mustParse([]int{0}), mustParse([]int{0, 1}), mustParse(nil)}
	want := []int64{10, 30, 35, 42, 22, 25, 15, 17}
	for i, f := range builtIn {
		if err := gv.Apply(f); err != nil {
			return err
		}
		if gv.View()[7][""] != want[i] { // I1（逐步）：组 () 总和符合八步表。
			return errors.New("selfcheck: grand-total mismatch")
		}
		for _, t := range gv.View() { // I3：现存 sum 必须为正（零值已移除、负值不可能）。
			for _, s := range t {
				if s <= 0 {
					return errors.New("selfcheck: non-positive sum retained")
				}
			}
		}
	}
	if !reflect.DeepEqual(gv.View(), batch(gs, builtIn)) { // I1（终态）：逐 (组,键) 等于批量重算。
		return errors.New("selfcheck: view != batch recomputation")
	}
	if !reflect.DeepEqual(idSet(gv.View()), map[int]bool{1: true, 3: true, 7: true}) { // I2。
		return errors.New("selfcheck: group-id set mismatch")
	}
	before := gv.View() // I4：非法 New 与三类坏事实全被拒，且拒绝前后逐字节不变。
	for _, g := range [][][]int{{}, {{0}, {0}}, {{0, 1}, {1, 0}}, {{3}}, {{-1}}} {
		if _, err := New(g); !errors.Is(err, ErrInvalidGroupingSets) {
			return errors.New("selfcheck: invalid grouping sets not rejected")
		}
	}
	for _, c := range []struct {
		f    Fact
		want error
	}{
		{Fact{A: "", B: "b", C: "c", M: 1}, ErrEmptyDimension},
		{Fact{A: "a", B: "b", C: "c", M: 0}, ErrZeroMeasure},
		{Fact{A: "x", B: "p", C: "u", M: -999}, ErrNegativeResult},
	} {
		if err := gv.Apply(c.f); !errors.Is(err, c.want) {
			return errors.New("selfcheck: bad fact not rejected")
		}
	}
	if !reflect.DeepEqual(gv.View(), before) {
		return errors.New("selfcheck: rejected operation mutated state")
	}
	if err := gv.Apply(Fact{A: "x", B: "q", C: "u", M: 1}); err != nil { // 拒绝后仍可正常使用。
		return err
	}
	return nil
}

func mustParse(d []int) gset.Group {
	g, ok := gset.Parse(d)
	if !ok {
		panic("selfcheck: bad internal dims")
	}
	return g
}

// batch 是独立批量重算参考实现：净事实投影 → 分组求和 → 归零即删。
func batch(gs []gset.Group, fs []Fact) map[int]map[string]int64 {
	out := map[int]map[string]int64{}
	for _, g := range gs {
		out[g.ID()] = map[string]int64{}
	}
	for _, f := range fs {
		vals := [gset.DimCount]string{f.A, f.B, f.C}
		for _, g := range gs {
			k := g.Key(vals)
			if s := out[g.ID()][k] + f.M; s == 0 {
				delete(out[g.ID()], k)
			} else {
				out[g.ID()][k] = s
			}
		}
	}
	return out
}

func idSet(w map[int]map[string]int64) map[int]bool {
	s := map[int]bool{}
	for id := range w {
		s[id] = true
	}
	return s
}
