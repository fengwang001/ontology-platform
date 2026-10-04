// Package lineage 维护列间派生边构成的有向无环图：边的四种语义、
// (src,dst) 唯一性、入边上限 16，以及加边时的环检测。
package lineage

import (
	"errors"
	"fmt"
)

// 细粒度哨兵错误，均可由 errors.Is 区分。
var (
	ErrInvalidKind  = errors.New("lineage: invalid edge kind")
	ErrEdgeExists   = errors.New("lineage: edge already exists")
	ErrEdgeNotFound = errors.New("lineage: edge not found")
	ErrInDegree     = errors.New("lineage: in-degree limit exceeded")
	ErrCycle        = errors.New("lineage: edge would create a cycle")
)

// MaxInDegree 是每列允许的最大入边数。
const MaxInDegree = 16

// Kind 是派生边的语义种类。
type Kind int

const (
	Copy Kind = iota // f(x) = x
	Mask             // f(x) = max(x-1, 0)
	Hash             // f(x) = max(x-2, 0)
	Agg              // f(x) = min(x, 2)
)

// Valid 报告 kind 是否越界。
func (k Kind) Valid() bool {
	return k >= Copy && k <= Agg
}

// Apply 计算经边传递后的级别。
func (k Kind) Apply(x int) int {
	switch k {
	case Copy:
		return x
	case Mask:
		return max(x-1, 0)
	case Hash:
		return max(x-2, 0)
	case Agg:
		return min(x, 2)
	}
	return x
}

// Graph 是派生边图。不是并发安全的，由上层引擎串行化访问。
type Graph struct {
	out map[string]map[string]Kind // src -> dst -> kind
	in  map[string]map[string]Kind // dst -> src -> kind
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		out: make(map[string]map[string]Kind),
		in:  make(map[string]map[string]Kind),
	}
}

// HasEdge 报告 src->dst 边是否存在。
func (g *Graph) HasEdge(src, dst string) bool {
	_, ok := g.out[src][dst]
	return ok
}

// EdgeKind 返回 src->dst 边的 kind。
func (g *Graph) EdgeKind(src, dst string) (Kind, bool) {
	k, ok := g.out[src][dst]
	return k, ok
}

// Out 返回 src 的全部出边（dst -> kind）的副本。
func (g *Graph) Out(src string) map[string]Kind {
	return cloneMap(g.out[src])
}

// In 返回 dst 的全部入边（src -> kind）的副本。
func (g *Graph) In(dst string) map[string]Kind {
	return cloneMap(g.in[dst])
}

// InDegree 返回 dst 的入边数。
func (g *Graph) InDegree(dst string) int {
	return len(g.in[dst])
}

// EdgeCount 返回图中边总数。
func (g *Graph) EdgeCount() int {
	n := 0
	for _, m := range g.out {
		n += len(m)
	}
	return n
}

func cloneMap(m map[string]Kind) map[string]Kind {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]Kind, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// AddEdge 登记 src->dst 派生边。调用方须保证 src != dst 且两端列已登记。
// 依次检查：kind 合法、边不重复、dst 入边未满 16、不成环。
func (g *Graph) AddEdge(src, dst string, k Kind) error {
	if !k.Valid() {
		return fmt.Errorf("%w: %d", ErrInvalidKind, int(k))
	}
	if g.HasEdge(src, dst) {
		return fmt.Errorf("%w: %q -> %q", ErrEdgeExists, src, dst)
	}
	if g.InDegree(dst) >= MaxInDegree {
		return fmt.Errorf("%w: %q already has %d in-edges", ErrInDegree, dst, MaxInDegree)
	}
	if g.reachable(dst, src) {
		return fmt.Errorf("%w: %q -> %q", ErrCycle, src, dst)
	}
	if g.out[src] == nil {
		g.out[src] = make(map[string]Kind)
	}
	g.out[src][dst] = k
	if g.in[dst] == nil {
		g.in[dst] = make(map[string]Kind)
	}
	g.in[dst][src] = k
	return nil
}

// RemoveEdge 删除 src->dst 边。
func (g *Graph) RemoveEdge(src, dst string) error {
	if !g.HasEdge(src, dst) {
		return fmt.Errorf("%w: %q -> %q", ErrEdgeNotFound, src, dst)
	}
	delete(g.out[src], dst)
	if len(g.out[src]) == 0 {
		delete(g.out, src)
	}
	delete(g.in[dst], src)
	if len(g.in[dst]) == 0 {
		delete(g.in, dst)
	}
	return nil
}

// reachable 报告从 from 沿出边能否到达 to（含 from == to）。
func (g *Graph) reachable(from, to string) bool {
	if from == to {
		return true
	}
	visited := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range g.out[cur] {
			if next == to {
				return true
			}
			if !visited[next] {
				visited[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}
