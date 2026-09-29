// Package graph 负责 DAG 的构建与校验：环检测（报真实闭合环路径）、
// 入度计算与拓扑分层。不依赖本工程其他任何包。
package graph

import "errors"

// StepID 是步骤标识。
type StepID string

// CycleError 表示输入中存在环；Path 是一条真实闭合环路径，首尾节点相同。
type CycleError struct {
	Path []StepID
}

func (e *CycleError) Error() string {
	return "graph: cycle detected: " + joinPath(e.Path)
}

func joinPath(p []StepID) string {
	out := ""
	for i, id := range p {
		if i > 0 {
			out += " -> "
		}
		out += string(id)
	}
	return out
}

// ErrMissingDep 在依赖指向未注册步骤时返回。
var ErrMissingDep = errors.New("graph: dependency points to unregistered step")

// Graph 是校验通过的不可变 DAG。
type Graph struct {
	ids    []StepID
	deps   map[StepID][]StepID // 节点 -> 它依赖的节点（边 dep -> id）
	next   map[StepID][]StepID // 节点 -> 依赖它的节点
	layers [][]StepID
}

// IDs 返回注册顺序的全部步骤。
func (g *Graph) IDs() []StepID { return append([]StepID(nil), g.ids...) }

// Layers 返回拓扑分层：第 0 层无依赖，每层的依赖全部位于更早的层。
func (g *Graph) Layers() [][]StepID {
	out := make([][]StepID, len(g.layers))
	for i, l := range g.layers {
		out[i] = append([]StepID(nil), l...)
	}
	return out
}

// ReverseLayers 返回逆拓扑层（最后一层在前），用于补偿调度。
func (g *Graph) ReverseLayers() [][]StepID {
	f := g.Layers()
	out := make([][]StepID, len(f))
	for i := range f {
		out[len(f)-1-i] = f[i]
	}
	return out
}

// Dependencies 返回某步骤直接依赖的节点。
func (g *Graph) Dependencies(id StepID) []StepID {
	return append([]StepID(nil), g.deps[id]...)
}

// Builder 增量构建 DAG。
type Builder struct {
	order []StepID
	known map[StepID]bool
	deps  map[StepID][]StepID
}

// NewBuilder 创建空构建器。
func NewBuilder() *Builder {
	return &Builder{known: map[StepID]bool{}, deps: map[StepID][]StepID{}}
}

// Add 注册一个步骤，deps 为它依赖的步骤。重复注册返回错误。
func (b *Builder) Add(id StepID, deps ...StepID) error {
	if b.known[id] {
		return errors.New("graph: duplicate step: " + string(id))
	}
	cp := append([]StepID(nil), deps...)
	b.known[id] = true
	b.order = append(b.order, id)
	b.deps[id] = cp
	return nil
}

// Build 校验依赖完整性与无环，并计算拓扑分层。
func (b *Builder) Build() (*Graph, error) {
	deps := map[StepID][]StepID{}
	next := map[StepID][]StepID{}
	for _, id := range b.order {
		ds := append([]StepID(nil), b.deps[id]...)
		for _, d := range ds {
			if !b.known[d] {
				return nil, ErrMissingDep
			}
			next[d] = append(next[d], id)
		}
		deps[id] = ds
	}

	const white, gray, black = 0, 1, 2
	color := map[StepID]int{}
	var stack []StepID
	var dfs func(StepID) error
	dfs = func(u StepID) error {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range deps[u] {
			switch color[v] {
			case white:
				if err := dfs(v); err != nil {
					return err
				}
			case gray:
				// 找到回边 v -> ... -> u -> v，截取闭合环路径并闭合到 v。
				path := []StepID{}
				for i := 0; i < len(stack); i++ {
					if stack[i] == v {
						path = append(path, stack[i:]...)
						break
					}
				}
				path = append(path, v)
				return &CycleError{Path: path}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	for _, id := range b.order {
		if color[id] == white {
			if err := dfs(id); err != nil {
				return nil, err
			}
		}
	}

	indeg := map[StepID]int{}
	for _, id := range b.order {
		indeg[id] = len(deps[id])
	}
	var layers [][]StepID
	remaining := map[StepID]bool{}
	for _, id := range b.order {
		remaining[id] = true
	}
	for len(remaining) > 0 {
		var layer []StepID
		for _, id := range b.order {
			if remaining[id] && indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 { // 理论上不会发生（环已被 DFS 拒绝）
			return nil, &CycleError{Path: []StepID{}}
		}
		for _, id := range layer {
			delete(remaining, id)
			for _, n := range next[id] {
				indeg[n]--
			}
		}
		layers = append(layers, layer)
	}

	return &Graph{ids: b.order, deps: deps, next: next, layers: layers}, nil
}
