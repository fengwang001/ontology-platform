// Package graph 构建有向无环任务图：幂等加边、环检测、拓扑分层。
package graph

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrNodeNotFound = errors.New("graph: referenced node does not exist")
	ErrCycle        = errors.New("graph: cycle detected")
)

// Graph 为有向图。边 from->to 表示 to 依赖 from（from 必须先完成）。
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{} // 后继（下游）
	pred  map[string]map[string]struct{} // 前置（上游）
}

func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
}

func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.succ[id] = map[string]struct{}{}
	g.pred[id] = map[string]struct{}{}
}

// AddEdge 记录 to 依赖 from；重复边幂等。任一节点不存在返回 ErrNodeNotFound。
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, to)
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

func (g *Graph) Nodes() []string {
	return sortedKeys(g.nodes, func(struct{}) bool { return true })
}

func (g *Graph) HasEdge(from, to string) bool {
	_, ok := g.succ[from][to]
	return ok
}

func (g *Graph) Preds(id string) []string {
	return sortedKeys(g.pred[id], func(struct{}) bool { return true })
}

func (g *Graph) Succs(id string) []string {
	return sortedKeys(g.succ[id], func(struct{}) bool { return true })
}

// Validate 执行前校验：依赖存在性已在 AddEdge 保证，这里做环检测。
// 环路径通过错误的 CyclePath 类型附带，首尾闭合且每跳都是真实输入边。
func (g *Graph) Validate() error {
	indeg := g.indegrees()
	queue := g.zeroIndeg(indeg)
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for s := range g.succ[cur] {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}
	if visited == len(g.nodes) {
		return nil
	}
	return &CycleError{Path: g.findCycle(indeg)}
}

func (g *Graph) indegrees() map[string]int {
	indeg := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	return indeg
}

func (g *Graph) zeroIndeg(indeg map[string]int) []string {
	return sortedKeys(indeg, func(d int) bool { return d == 0 })
}

// findCycle 在残留子图（入度>0，必含环）上 DFS，回溯出闭合环路径。
func (g *Graph) findCycle(indeg map[string]int) []string {
	onCycle := map[string]bool{}
	for id, d := range indeg {
		if d > 0 {
			onCycle[id] = true
		}
	}
	color := map[string]int{} // 0=白 1=灰 2=黑
	var stack []string
	var found []string
	roots := sortedKeys(onCycle, func(v bool) bool { return v })
	var dfs func(string) bool
	dfs = func(u string) bool {
		color[u] = 1
		stack = append(stack, u)
		next := g.Succs(u)
		for _, v := range next {
			if !onCycle[v] {
				continue
			}
			if color[v] == 1 {
				for i, x := range stack {
					if x == v {
						found = append([]string{}, stack[i:]...)
						found = append(found, v)
						return true
					}
				}
			}
			if color[v] == 0 && dfs(v) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = 2
		return false
	}
	for _, r := range roots {
		if color[r] == 0 && dfs(r) {
			return found
		}
	}
	return found
}

func sortedKeys[V any](m map[string]V, keep func(V) bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if keep(v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// CycleError 携带真实环路径：Path[0]==Path[len-1]，相邻元素间存在输入边。
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return fmt.Sprintf("%s: %v", ErrCycle, e.Path)
}

func (e *CycleError) Unwrap() error { return ErrCycle }

// Layers 返回拓扑分层（同层内无依赖，可并行），调用前应已通过 Validate。
func (g *Graph) Layers() [][]string {
	indeg := g.indegrees()
	var layers [][]string
	frontier := g.zeroIndeg(indeg)
	for len(frontier) > 0 {
		layers = append(layers, append([]string{}, frontier...))
		var next []string
		for _, u := range frontier {
			for s := range g.succ[u] {
				indeg[s]--
				if indeg[s] == 0 {
					next = append(next, s)
				}
			}
		}
		sort.Strings(next)
		frontier = next
	}
	return layers
}
