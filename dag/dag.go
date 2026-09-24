// Package dag 维护视图依赖图：AddView 成环检测、脏集（传递闭包）计算、拓扑序生成。
package dag

import (
	"errors"
	"slices"
)

var (
	ErrCycle      = errors.New("dag: dependency cycle")
	ErrDupView    = errors.New("dag: view already exists")
	ErrUnknownDep = errors.New("dag: unknown dependency")
	ErrNameClash  = errors.New("dag: name already used")
)

// Graph 记录视图与其直接依赖（依赖可以是基底或其他视图）。
type Graph struct {
	isBase   map[string]bool
	deps     map[string][]string // view -> 直接依赖名
	children map[string][]string // 依赖名 -> 直接依赖它的视图
	names    []string            // 全部视图名（声明序）
}

func New() *Graph {
	return &Graph{
		isBase:   map[string]bool{},
		deps:     map[string][]string{},
		children: map[string][]string{},
	}
}

func (g *Graph) AddBase(name string) error {
	if g.isBase[name] || g.IsView(name) {
		return ErrNameClash
	}
	g.isBase[name] = true
	return nil
}

func (g *Graph) IsView(name string) bool   { _, ok := g.deps[name]; return ok }
func (g *Graph) IsBase(name string) bool   { return g.isBase[name] }
func (g *Graph) NumViews() int             { return len(g.names) }
func (g *Graph) Views() []string           { return slices.Clone(g.names) }
func (g *Graph) Deps(name string) []string { return g.deps[name] }

// AddView 声明视图及其依赖。任一校验失败都不留痕迹。
func (g *Graph) AddView(name string, deps []string) error {
	if g.IsView(name) || g.isBase[name] {
		return ErrDupView
	}
	deps = dedupe(deps)
	for _, d := range deps {
		if d != name && !g.IsView(d) && !g.isBase[d] {
			return ErrUnknownDep
		}
	}
	g.deps[name] = deps
	for _, d := range deps {
		g.children[d] = append(g.children[d], name)
	}
	if g.cycleFrom(name) { // 整体回滚
		delete(g.deps, name)
		for _, d := range deps {
			ch := g.children[d]
			g.children[d] = ch[:len(ch)-1]
		}
		return ErrCycle
	}
	g.names = append(g.names, name)
	return nil
}

// cycleFrom 检测从 name 沿依赖边出发能否回到 name。
func (g *Graph) cycleFrom(name string) bool {
	seen := map[string]bool{}
	var dfs func(string) bool
	dfs = func(n string) bool {
		if n == name {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, d := range g.deps[n] {
			if g.IsView(d) && dfs(d) {
				return true
			}
		}
		return false
	}
	for _, d := range g.deps[name] {
		if g.IsView(d) && dfs(d) {
			return true
		}
	}
	return false
}

// Dirty 返回变更基底集合的传递闭包后代（含多级）。
func (g *Graph) Dirty(changed []string) map[string]bool {
	dirty := map[string]bool{}
	queue := slices.Clone(changed)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, c := range g.children[n] {
			if !dirty[c] {
				dirty[c] = true
				queue = append(queue, c)
			}
		}
	}
	return dirty
}

// Topo 对脏集生成拓扑序：重复选取「全部视图依赖都已刷新或本批不需刷新」
// 的脏视图中名字典序最小者。
func (g *Graph) Topo(dirty map[string]bool) ([]string, error) {
	done := map[string]bool{}
	rest := map[string]bool{}
	for v := range dirty {
		rest[v] = true
	}
	var order []string
	for len(rest) > 0 {
		var ready []string
		for v := range rest {
			ok := true
			for _, d := range g.deps[v] {
				if g.IsView(d) && dirty[d] && !done[d] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, v)
			}
		}
		if len(ready) == 0 {
			return nil, ErrCycle
		}
		slices.Sort(ready)
		v := ready[0]
		order = append(order, v)
		done[v] = true
		delete(rest, v)
	}
	return order, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
