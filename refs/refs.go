// Package refs 保存 child -> parent 的引用边、引用策略，并按 parent 维护入边索引。
package refs

import (
	"errors"
	"sort"

	"ontology/store"
)

// MaxOutDegree 限制每条记录的出边数。
const MaxOutDegree = 8

// ErrTooManyRefs 在出边超过 8 条时返回。
var ErrTooManyRefs = errors.New("refs: out-degree exceeds 8")

// Policy 是引用策略。
type Policy int

const (
	Cascade Policy = iota + 1
	SetNull
	Restrict
)

// Edge 是一条带策略的引用边。
type Edge struct {
	Child  int64
	Parent int64
	Policy Policy
}

// Graph 是引用图；自身不加锁，由 erase.Executor 统一加锁。
type Graph struct {
	out map[int64]map[int64]Policy
	in  map[int64]map[int64]Policy
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		out: map[int64]map[int64]Policy{},
		in:  map[int64]map[int64]Policy{},
	}
}

// Add 登记一条边。调用方（erase.Executor）负责参数、时钟与端点存在性校验；
// 此处仅判断重复边与出边上限。
func (g *Graph) Add(e Edge) error {
	outs := g.out[e.Child]
	if outs != nil {
		if _, dup := outs[e.Parent]; dup {
			return store.ErrExists
		}
		if len(outs) >= MaxOutDegree {
			return ErrTooManyRefs
		}
	}
	if outs == nil {
		outs = map[int64]Policy{}
		g.out[e.Child] = outs
	}
	outs[e.Parent] = e.Policy
	ins := g.in[e.Parent]
	if ins == nil {
		ins = map[int64]Policy{}
		g.in[e.Parent] = ins
	}
	ins[e.Child] = e.Policy
	return nil
}

// Remove 删除一条边；不存在返回 store.ErrNotFound。
func (g *Graph) Remove(child, parent int64) error {
	outs := g.out[child]
	if outs == nil {
		return store.ErrNotFound
	}
	if _, ok := outs[parent]; !ok {
		return store.ErrNotFound
	}
	delete(outs, parent)
	if len(outs) == 0 {
		delete(g.out, child)
	}
	delete(g.in[parent], child)
	if len(g.in[parent]) == 0 {
		delete(g.in, parent)
	}
	return nil
}

// Has 报告边是否存在及其策略。
func (g *Graph) Has(child, parent int64) (Policy, bool) {
	if outs := g.out[child]; outs != nil {
		p, ok := outs[parent]
		return p, ok
	}
	return 0, false
}

// InEdges 返回 parent 的全部入边，按 child 升序。
func (g *Graph) InEdges(parent int64) []Edge {
	ins := g.in[parent]
	edges := make([]Edge, 0, len(ins))
	for child, p := range ins {
		edges = append(edges, Edge{Child: child, Parent: parent, Policy: p})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].Child < edges[j].Child })
	return edges
}

// DeleteVertex 删除顶点的全部关联边。
func (g *Graph) DeleteVertex(id int64) {
	for parent := range g.out[id] {
		delete(g.in[parent], id)
		if len(g.in[parent]) == 0 {
			delete(g.in, parent)
		}
	}
	delete(g.out, id)
	for child := range g.in[id] {
		delete(g.out[child], id)
		if len(g.out[child]) == 0 {
			delete(g.out, child)
		}
	}
	delete(g.in, id)
}

// All 返回全部边（按 (child,parent) 升序，供朴素模拟使用）。
func (g *Graph) All() []Edge {
	edges := make([]Edge, 0)
	for child, outs := range g.out {
		for parent, p := range outs {
			edges = append(edges, Edge{Child: child, Parent: parent, Policy: p})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Child != edges[j].Child {
			return edges[i].Child < edges[j].Child
		}
		return edges[i].Parent < edges[j].Parent
	})
	return edges
}
