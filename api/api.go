// Package api 是行级过滤视图增量维护的对外入口（依赖 fview）。
package api

import (
	"fmt"
	"maps"

	"ontology/fpred"
	"ontology/fview"
)

// FilteredView 是对外的过滤视图（fview.View 自带并发安全）。
type FilteredView struct{ v *fview.View }

// New 构造谓词 lo <= Val < hi 的视图；lo >= hi 返回 fpred.ErrInvalidRange。
func New(lo, hi int64) (*FilteredView, error) {
	v, err := fview.New(lo, hi)
	if err != nil {
		return nil, err
	}
	return &FilteredView{v: v}, nil
}

// Apply 按顺序处理一批源变更，返回本批变更日志；任一变更被拒则整批不生效。
func (f *FilteredView) Apply(changes []fpred.Change) ([]fpred.Out, error) {
	return f.v.Apply(changes)
}

// View 返回下游应用全部变更日志后的视图。
func (f *FilteredView) View() map[string]int64 {
	view, _ := f.v.Snapshot()
	return view
}

// Source 返回当前源表。
func (f *FilteredView) Source() map[string]int64 {
	_, src := f.v.Snapshot()
	return src
}

// SelfCheck 运行内置八步序列，核验四条不变量；全部成立返回 nil。
func (f *FilteredView) SelfCheck() error {
	g, err := New(10, 20)
	if err != nil {
		return err
	}
	steps := []struct {
		ch   fpred.Change
		want []fpred.Out
	}{
		{fpred.Change{Kind: fpred.Insert, After: fpred.Row{ID: "x", Val: 5}}, nil},
		{fpred.Change{Kind: fpred.Insert, After: fpred.Row{ID: "y", Val: 12}}, []fpred.Out{{Row: fpred.Row{ID: "y", Val: 12}, Add: true}}},
		{fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: "x", Val: 5}, After: fpred.Row{ID: "x", Val: 15}}, []fpred.Out{{Row: fpred.Row{ID: "x", Val: 15}, Add: true}}},
		{fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: "y", Val: 12}, After: fpred.Row{ID: "y", Val: 12}}, nil},
		{fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: "y", Val: 12}, After: fpred.Row{ID: "y", Val: 20}}, []fpred.Out{{Row: fpred.Row{ID: "y", Val: 12}}}},
		{fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: "y", Val: 20}, After: fpred.Row{ID: "y", Val: 3}}, nil},
		{fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: "x", Val: 15}, After: fpred.Row{ID: "x", Val: 10}}, []fpred.Out{{Row: fpred.Row{ID: "x", Val: 15}}, {Row: fpred.Row{ID: "x", Val: 10}, Add: true}}},
		{fpred.Change{Kind: fpred.Delete, Before: fpred.Row{ID: "y", Val: 3}}, nil},
	}
	all := []fpred.Out{}
	for i, st := range steps {
		got, e := g.Apply([]fpred.Change{st.ch})
		if e != nil || fmt.Sprint(got) != fmt.Sprint(st.want) {
			return fmt.Errorf("selfcheck step %d: got %v err %v", i+1, got, e)
		}
		all = append(all, got...)
		if !maps.Equal(g.View(), recompute(g.Source(), 10, 20)) { // 不变量1
			return fmt.Errorf("selfcheck step %d: view != recompute", i+1)
		}
	}
	if !maps.Equal(g.View(), map[string]int64{"x": 10}) {
		return fmt.Errorf("selfcheck final view %v", g.View())
	}
	if err := replayCheck(all); err != nil { // 不变量2
		return err
	}
	return rejectedNoTrace(g) // 不变量4（不变量3 由第 4 步 want=nil 钉住）
}

func recompute(src map[string]int64, lo, hi int64) map[string]int64 {
	p, _ := fpred.NewPred(lo, hi)
	out := map[string]int64{}
	for id, val := range src {
		if p.Holds(fpred.Row{ID: id, Val: val}) {
			out[id] = val
		}
	}
	return out
}

// replayCheck 让下游按序应用日志的每个前缀，校验唯一性与撤回精确匹配。
func replayCheck(log []fpred.Out) error {
	d := map[string]int64{}
	for i, o := range log {
		got, ok := d[o.Row.ID]
		if o.Add == ok || (!o.Add && got != o.Row.Val) {
			return fmt.Errorf("selfcheck log prefix %d inconsistent", i)
		}
		if o.Add {
			d[o.Row.ID] = o.Row.Val
		} else {
			delete(d, o.Row.ID)
		}
	}
	return nil
}

// rejectedNoTrace 注入四类非法变更，断言错误互不相同、状态不动、之后仍可用。
func rejectedNoTrace(g *FilteredView) error {
	if _, e := g.Apply([]fpred.Change{{Kind: fpred.Insert, After: fpred.Row{ID: "z", Val: 15}}}); e != nil {
		return e
	}
	good := fpred.Change{Kind: fpred.Insert, After: fpred.Row{ID: "g", Val: 15}}
	bad := []fpred.Change{
		{Kind: fpred.Update, Before: fpred.Row{ID: "z", Val: 15}, After: fpred.Row{ID: "q", Val: 2}}, // 变更非法
		{Kind: fpred.Insert, After: fpred.Row{ID: "z", Val: 1}},                                      // 主键冲突
		{Kind: fpred.Delete, Before: fpred.Row{ID: "nope", Val: 1}},                                  // 主键不存在
		{Kind: fpred.Update, Before: fpred.Row{ID: "z", Val: 9}, After: fpred.Row{ID: "z", Val: 16}}, // 前像不符
	}
	seen := map[error]bool{}
	for i, b := range bad {
		v0, s0 := g.View(), g.Source()
		outs, e := g.Apply([]fpred.Change{b, good})
		if e == nil || seen[e] || len(outs) != 0 {
			return fmt.Errorf("selfcheck bad %d: err %v", i, e)
		}
		seen[e] = true
		if !maps.Equal(v0, g.View()) || !maps.Equal(s0, g.Source()) {
			return fmt.Errorf("selfcheck bad %d left a trace", i)
		}
	}
	if _, e := g.Apply([]fpred.Change{good}); e != nil {
		return fmt.Errorf("selfcheck unusable after rejection: %w", e)
	}
	return nil
}
