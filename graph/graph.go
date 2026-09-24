// Package graph 构建有向无环任务图，提供幂等加边、依赖校验、
// 环检测（含自环）与拓扑分层。
package graph

import (
	"errors"
	"fmt"
	"sort"
)

// ErrMissingNode 表示边引用了未注册的任务。
var ErrMissingNode = errors.New("graph: edge references missing node")

// ErrCycle 表示图中存在环，错误附带一条真实闭合环路径。
var ErrCycle = errors.New("graph: cycle detected")

// Graph 是任务依赖图。边 from -> to 表示 to 依赖 from。
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{}
	pred  map[string]map[string]struct{}
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
}

// AddNode 注册任务节点，重复注册幂等。
func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.succ[id] = map[string]struct{}{}
	g.pred[id] = map[string]struct{}{}
}

// AddEdge 表示 to 依赖 from：from 必须先于 to 执行。重复加边幂等。
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingNode, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingNode, to)
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

// Nodes 按 ID 字典序返回全部节点。
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Succ 返回 from 的直接后继（依赖 from 的任务），字典序。
func (g *Graph) Succ(from string) []string {
	return keys(g.succ[from])
}

// Pred 返回 to 的直接前驱（to 依赖的任务），字典序。
func (g *Graph) Pred(to string) []string {
	return keys(g.pred[to])
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CycleError 描述检测到的环：Path 中相邻元素构成边，首尾相同。
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%v: %v", ErrCycle, e.Path)
}
func (e *CycleError) Unwrap() error { return ErrCycle }

// Validate 检查依赖节点存在性（AddEdge 已保证）并检测环。
func (g *Graph) Validate() error {
	return g.checkCycle()
}

// checkCycle 用 DFS 三色标记，在回溯链中找到闭合环路径。
func (g *Graph) checkCycle() error {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var stack []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range g.Succ(u) {
			if color[v] == gray {
				i := 0
				for stack[i] != v {
					i++
				}
				return append(append([]string{}, stack[i:]...), v)
			}
			if color[v] == white {
				if p := dfs(v); p != nil {
					return p
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	for _, id := range g.Nodes() {
		if color[id] == white {
			if p := dfs(id); p != nil {
				return &CycleError{Path: p}
			}
		}
	}
	return nil
}

// Layers 返回拓扑分层：第 0 层无依赖，每层仅依赖更早层。
// 图有环时返回 ErrCycle。
func (g *Graph) Layers() ([][]string, error) {
	if err := g.checkCycle(); err != nil {
		return nil, err
	}
	indeg := map[string]int{}
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	var layers [][]string
	remaining := len(g.nodes)
	for remaining > 0 {
		var layer []string
		for _, id := range g.Nodes() {
			if indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil, ErrCycle
		}
		sort.Strings(layer)
		layers = append(layers, layer)
		for _, id := range layer {
			indeg[id] = -1
			remaining--
			for _, s := range g.Succ(id) {
				indeg[s]--
			}
		}
	}
	return layers, nil
}
