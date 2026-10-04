// Package refs 维护记录间的引用边及其级联策略。
// Graph 不是并发安全的，调用方（erase.Executor）负责串行化。
package refs

import (
	"slices"

	"ontology/store"
)

// MaxOut 是每条记录允许的出边上限。
const MaxOut = 8

type Policy int

const (
	Cascade Policy = iota
	SetNull
	Restrict
)

func (p Policy) Valid() bool { return p >= Cascade && p <= Restrict }

func (p Policy) String() string {
	switch p {
	case Cascade:
		return "Cascade"
	case SetNull:
		return "SetNull"
	case Restrict:
		return "Restrict"
	}
	return "Unknown"
}

// Edge 表示 child 引用 parent 的一条有向边。
type Edge struct {
	Child  int64
	Parent int64
}

type Graph struct {
	pol map[Edge]Policy
	out map[int64]map[int64]Policy
	in  map[int64]map[int64]Policy
}

func New() *Graph {
	return &Graph{
		pol: make(map[Edge]Policy),
		out: make(map[int64]map[int64]Policy),
		in:  make(map[int64]map[int64]Policy),
	}
}

// Add 登记一条边；调用方需先确认两端记录存在。
func (g *Graph) Add(child, parent int64, p Policy) error {
	if child == parent || !p.Valid() {
		return store.ErrInvalidArgument
	}
	edge := Edge{Child: child, Parent: parent}
	if _, ok := g.pol[edge]; ok {
		return store.ErrExists
	}
	if len(g.out[child]) >= MaxOut {
		return store.ErrTooManyOutgoing
	}
	g.pol[edge] = p
	if g.out[child] == nil {
		g.out[child] = make(map[int64]Policy)
	}
	g.out[child][parent] = p
	if g.in[parent] == nil {
		g.in[parent] = make(map[int64]Policy)
	}
	g.in[parent][child] = p
	return nil
}

func (g *Graph) Remove(child, parent int64) error {
	edge := Edge{Child: child, Parent: parent}
	if _, ok := g.pol[edge]; !ok {
		return store.ErrNotFound
	}
	g.remove(edge)
	return nil
}

func (g *Graph) remove(edge Edge) {
	delete(g.pol, edge)
	delete(g.out[edge.Child], edge.Parent)
	if len(g.out[edge.Child]) == 0 {
		delete(g.out, edge.Child)
	}
	delete(g.in[edge.Parent], edge.Child)
	if len(g.in[edge.Parent]) == 0 {
		delete(g.in, edge.Parent)
	}
}

func (g *Graph) Has(child, parent int64) bool {
	_, ok := g.pol[Edge{Child: child, Parent: parent}]
	return ok
}

func (g *Graph) Len() int { return len(g.pol) }

func (g *Graph) Policy(child, parent int64) (Policy, bool) {
	p, ok := g.pol[Edge{Child: child, Parent: parent}]
	return p, ok
}

// Out 返回 child 的全部出边（按 parent 升序）。
func (g *Graph) Out(child int64) []Edge {
	parents := g.out[child]
	out := make([]Edge, 0, len(parents))
	for parent := range parents {
		out = append(out, Edge{Child: child, Parent: parent})
	}
	slices.SortFunc(out, func(a, b Edge) int { return compare(a.Parent, b.Parent) })
	return out
}

// In 返回指向 parent 的全部入边（按 child 升序），供级联遍历。
func (g *Graph) In(parent int64) []Edge {
	children := g.in[parent]
	out := make([]Edge, 0, len(children))
	for child := range children {
		out = append(out, Edge{Child: child, Parent: parent})
	}
	slices.SortFunc(out, func(a, b Edge) int { return compare(a.Child, b.Child) })
	return out
}

// RemoveRecord 删除与某记录相关的全部边（出边与入边）。
func (g *Graph) RemoveRecord(id int64) {
	for _, edge := range g.Out(id) {
		g.remove(edge)
	}
	for _, edge := range g.In(id) {
		g.remove(edge)
	}
}

func compare(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
