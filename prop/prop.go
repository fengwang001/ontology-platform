// Package prop 维护节点当前值，按层级分轮传播，产出变更日志。依赖 dag。
package prop

import (
	"errors"
	"fmt"
	"sort"

	"ontology/dag"
)

var ErrNotSource = errors.New("prop: cannot set a derived node")

type Change struct {
	Name string
	Old  int64
	New  int64
}

type Engine struct {
	g       *dag.Graph
	values  map[string]int64
	checked int // 最近一次 Apply 被检查过的节点数（非导出，不进公开接口）
}

func New(g *dag.Graph) *Engine {
	e := &Engine{g: g, values: map[string]int64{}}
	names := g.Names()
	sort.Slice(names, func(i, j int) bool { // 拓扑序：层级升序
		return g.Level(names[i]) < g.Level(names[j])
	})
	for _, n := range names {
		if g.IsSrc(n) {
			e.values[n] = g.Def(n).K
		} else {
			e.values[n] = e.eval(g.Def(n))
		}
	}
	return e
}

func (e *Engine) eval(d dag.Def) int64 {
	switch d.Kind {
	case "scale":
		return d.K * e.values[d.Inputs[0]]
	case "add":
		return e.values[d.Inputs[0]] + d.K
	case "sum":
		var s int64
		for _, in := range d.Inputs {
			s += e.values[in]
		}
		return s
	}
	return 0
}

// Apply 同时设置若干源节点并分轮传播。任一键被拒则整批不生效。
func (e *Engine) Apply(sets map[string]int64) ([]Change, error) {
	for n := range sets { // 先查全部键存在
		if !e.g.Has(n) {
			return nil, fmt.Errorf("%w: %q", dag.ErrUnknownNode, n)
		}
	}
	for n := range sets { // 再查都是源节点
		if !e.g.IsSrc(n) {
			return nil, fmt.Errorf("%w: %q", ErrNotSource, n)
		}
	}
	e.checked = 0
	var log []Change
	changed := map[string]bool{}
	pending := map[string]bool{} // 候选调度集：有输入变过的派生节点
	keys := make([]string, 0, len(sets))
	for n := range sets {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	for _, n := range keys { // 第 0 轮：源节点按名序
		e.checked++
		if sets[n] == e.values[n] {
			continue // 同值视为没变
		}
		log = append(log, Change{n, e.values[n], sets[n]})
		e.values[n] = sets[n]
		changed[n] = true
		for _, c := range e.g.Consumers(n) {
			if !pending[c] {
				pending[c] = true
				e.checked++
			}
		}
	}
	for len(pending) > 0 { // 按层级分轮：每轮取当前最小层
		minLvl := -1
		for n := range pending {
			if l := e.g.Level(n); minLvl < 0 || l < minLvl {
				minLvl = l
			}
		}
		var batch []string
		for n := range pending {
			if e.g.Level(n) == minLvl {
				batch = append(batch, n)
				delete(pending, n)
				e.checked++
			}
		}
		sort.Strings(batch) // 同轮按名字典序
		for _, n := range batch {
			e.checked++
			v := e.eval(e.g.Def(n))
			if v == e.values[n] {
				continue // 值没变不输出、不触发下游
			}
			log = append(log, Change{n, e.values[n], v})
			e.values[n] = v
			changed[n] = true
			for _, c := range e.g.Consumers(n) {
				if !pending[c] && !changed[c] {
					pending[c] = true
					e.checked++
				}
			}
		}
	}
	return log, nil
}

func (e *Engine) Value(name string) (int64, bool) {
	v, ok := e.values[name]
	return v, ok
}

func (e *Engine) Snapshot() map[string]int64 {
	out := make(map[string]int64, len(e.values))
	for n, v := range e.values {
		out[n] = v
	}
	return out
}
