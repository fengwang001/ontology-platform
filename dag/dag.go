// Package dag 维护视图依赖图：节点、依赖边、拓扑排序与环检测。不依赖其他包。
package dag

import "errors"

// ErrCycle 表示加入的依赖边会构成环。
var ErrCycle = errors.New("dag: dependency cycle")

// Graph 是有向图：边 name -> dep 表示 name 依赖 dep。
type Graph struct {
	deps       map[string][]string // 视图 -> 它的依赖
	dependents map[string][]string // 名字 -> 依赖它的视图（反向边）
}

func New() *Graph {
	return &Graph{deps: map[string][]string{}, dependents: map[string][]string{}}
}

// Has 报告 name 是否已注册。
func (g *Graph) Has(name string) bool { _, ok := g.deps[name]; return ok }

// Add 注册节点及其依赖边（允许依赖未注册名）。若新边与已有边构成环，
// 返回 ErrCycle 且图不变。
func (g *Graph) Add(name string, deps []string) error {
	for _, d := range deps {
		if d == name || g.reaches(d, name) {
			return ErrCycle
		}
	}
	g.deps[name] = append([]string(nil), deps...)
	for _, d := range deps {
		g.dependents[d] = append(g.dependents[d], name)
	}
	return nil
}

// reaches 报告从 from 沿依赖边能否到达 target（只经过已注册节点）。
func (g *Graph) reaches(from, target string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == target {
			return true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, g.deps[n]...)
	}
	return false
}

// Deps 返回 name 的依赖列表（可能含未注册名）。
func (g *Graph) Deps(name string) []string { return g.deps[name] }

// Closure 返回 name 的全部下游传递闭包（不含 name 自身）。
func (g *Graph) Closure(name string) map[string]bool {
	out := map[string]bool{}
	stack := append([]string(nil), g.dependents[name]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[n] {
			continue
		}
		out[n] = true
		stack = append(stack, g.dependents[n]...)
	}
	return out
}

// Topo 对 subset 中的节点做 Kahn 拓扑排序（依赖先于被依赖者），
// 只考虑两端都在 subset 内的边；子图成环时返回 ErrCycle。
func (g *Graph) Topo(subset map[string]bool) ([]string, error) {
	indeg := map[string]int{}
	for n := range subset {
		indeg[n] = 0
	}
	for n := range subset {
		for _, d := range g.deps[n] {
			if subset[d] {
				indeg[n]++
			}
		}
	}
	var queue []string
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	var order []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range g.dependents[n] {
			if !subset[m] {
				continue
			}
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if len(order) != len(subset) {
		return nil, ErrCycle
	}
	return order, nil
}
