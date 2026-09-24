// Package rfr 按 dag 给出的拓扑序执行批量重算、产出变更日志、维护当前全量视图。
package rfr

import (
	"maps"

	"ontology/dag"
)

// Change 是一对变更日志：-(Name,Old) 然后 +(Name,New)。
type Change struct {
	Name     string
	Old, New int64
}

// Refresher 登记基底变更并批量刷新视图。
type Refresher struct {
	g      *dag.Graph
	bases  map[string]int64
	views  map[string]int64
	pend   map[string]int64 // 本批登记的基底写入，同名只留末值
	recomp int              // 最近一次 Refresh 实际执行的视图重算次数（非导出）
}

func New(g *dag.Graph) *Refresher {
	return &Refresher{
		g:     g,
		bases: map[string]int64{},
		views: map[string]int64{},
		pend:  map[string]int64{},
	}
}

// SetBase 只登记变更并标脏（延迟到 Refresh），同批同名只留末值。
func (r *Refresher) SetBase(name string, v int64) { r.pend[name] = v }

// Refresh 把当前累积的一批变更一次处理完：每个脏视图按拓扑序恰好重算一次。
func (r *Refresher) Refresh() ([]Change, error) {
	changed := make([]string, 0, len(r.pend))
	for name, v := range r.pend {
		r.bases[name] = v
		changed = append(changed, name)
	}
	r.pend = map[string]int64{}
	order, err := r.g.Topo(r.g.Dirty(changed))
	if err != nil {
		return nil, err
	}
	r.recomp = len(order)
	log := make([]Change, 0, len(order))
	for _, v := range order {
		var sum int64
		for _, d := range r.g.Deps(v) {
			if r.g.IsView(d) {
				sum += r.views[d] // 已刷视图取新值，未脏视图取现值
			} else {
				sum += r.bases[d] // 基底取末值
			}
		}
		log = append(log, Change{Name: v, Old: r.views[v], New: sum})
		r.views[v] = sum
	}
	return log, nil
}

// View 返回全部视图的当前值（副本）。
func (r *Refresher) View() map[string]int64 {
	out := map[string]int64{}
	for _, v := range r.g.Views() {
		out[v] = r.views[v]
	}
	return out
}

// Base 返回基底当前值。
func (r *Refresher) Base(name string) int64 { return r.bases[name] }

// cloneView 供测试与自检复制视图（不导出计数器）。
func cloneView(m map[string]int64) map[string]int64 { return maps.Clone(m) }
