// Package graph 构建有向无环任务图：去重加边、环检测、拓扑分层。
package graph

import (
	"fmt"
	"sort"

	"ontology/fail"
)

// ID 是任务节点标识。
type ID string

// Graph 是有向图；边 from→to 表示 to 依赖 from。
type Graph struct {
	nodes map[ID]struct{}
	succ  map[ID]map[ID]struct{}
	pred  map[ID]map[ID]struct{}
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		nodes: map[ID]struct{}{},
		succ:  map[ID]map[ID]struct{}{},
		pred:  map[ID]map[ID]struct{}{},
	}
}

// AddNode 幂等加入节点。
func (g *Graph) AddNode(id ID) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.succ[id] = map[ID]struct{}{}
	g.pred[id] = map[ID]struct{}{}
}

// AddEdge 幂等加边；端点不存在返回 fail.ErrMissingDep。
func (g *Graph) AddEdge(from, to ID) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %s", fail.ErrMissingDep, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %s", fail.ErrMissingDep, to)
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

// Nodes 按字典序返回全部节点。
func (g *Graph) Nodes() []ID {
	ids := make([]ID, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Succ / Pred 按字典序返回直接后继 / 前驱。
func (g *Graph) Succ(id ID) []ID { return keys(g.succ[id]) }
func (g *Graph) Pred(id ID) []ID { return keys(g.pred[id]) }

func keys(m map[ID]struct{}) []ID {
	ids := make([]ID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// CycleError 携带一条真实环路径：相邻元素间的边都在图中，首尾相同。
type CycleError struct {
	Path []ID
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%s: %v", fail.ErrCycle, e.Path)
}
func (e *CycleError) Unwrap() error { return fail.ErrCycle }

// Check 在执行前校验：检出环（含自环），返回 *CycleError。
func (g *Graph) Check() error {
	const white, gray, black = 0, 1, 2
	color := map[ID]int{}
	var stack []ID
	var dfs func(ID) error
	dfs = func(u ID) error {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range g.Succ(u) {
			switch color[v] {
			case gray:
				k := len(stack) - 1
				for stack[k] != v {
					k--
				}
				path := append([]ID{}, stack[k:]...)
				path = append(path, v)
				return &CycleError{Path: path}
			case white:
				if err := dfs(v); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	for _, id := range g.Nodes() {
		if color[id] == white {
			if err := dfs(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Layers 返回拓扑分层（Kahn），每层内按 ID 升序；有环时返回错误。
func (g *Graph) Layers() ([][]ID, error) {
	if err := g.Check(); err != nil {
		return nil, err
	}
	indeg := map[ID]int{}
	for _, id := range g.Nodes() {
		indeg[id] = len(g.pred[id])
	}
	var layers [][]ID
	remaining := len(g.nodes)
	for remaining > 0 {
		var layer []ID
		for _, id := range g.Nodes() {
			if indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		for _, id := range layer {
			indeg[id] = -1
			remaining--
			for _, v := range g.Succ(id) {
				indeg[v]--
			}
		}
		layers = append(layers, layer)
	}
	return layers, nil
}
