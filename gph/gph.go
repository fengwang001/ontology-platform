// Package gph 维护事务依赖图：登记/暂存、激活、环检测与入度（未回放依赖数）。
package gph

import (
	"container/heap"
	"errors"
	"sort"
)

var (
	ErrInvalidID = errors.New("gph: invalid id")
	ErrSelfDep   = errors.New("gph: self dependency")
	ErrDuplicate = errors.New("gph: duplicate commit")
	ErrCycle     = errors.New("gph: dependency cycle")
	ErrFull      = errors.New("gph: transaction limit reached")
)

// intHeap 是可回放事务的最小堆，保证回放顺序可复现。
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Graph 是事务依赖图。非并发安全，由上层加锁。
type Graph struct {
	max        int
	deps       map[int][]int
	edges      map[int]map[int]bool // 已登记事务 -> 已登记的依赖（查环用）
	staged     map[int]bool
	indeg      map[int]int   // 已激活事务的未回放依赖数
	dependents map[int][]int // 依赖 -> 依赖它的已激活事务
	replayed   map[int]bool
	ready      intHeap // 入度为 0 的可回放事务
}

// New 创建最多容纳 maxTxns 个事务的空图。
func New(maxTxns int) *Graph {
	return &Graph{
		max:        maxTxns,
		deps:       map[int][]int{},
		edges:      map[int]map[int]bool{},
		staged:     map[int]bool{},
		indeg:      map[int]int{},
		dependents: map[int][]int{},
		replayed:   map[int]bool{},
	}
}

// Commit 登记事务及其依赖；任一校验失败则整体拒绝、不留痕迹。
func (g *Graph) Commit(id int, deps []int) error {
	if id <= 0 {
		return ErrInvalidID
	}
	if _, ok := g.deps[id]; ok {
		return ErrDuplicate
	}
	seen := make(map[int]bool, len(deps))
	ds := make([]int, 0, len(deps))
	for _, d := range deps {
		if d <= 0 {
			return ErrInvalidID
		}
		if d == id {
			return ErrSelfDep
		}
		if !seen[d] {
			seen[d] = true
			ds = append(ds, d)
		}
	}
	if len(g.deps) >= g.max {
		return ErrFull
	}
	// 在克隆边集上模拟本次登记与连带激活产生的新边，查环通过后才落地。
	edges := make(map[int]map[int]bool, len(g.edges)+1)
	for n, es := range g.edges {
		c := make(map[int]bool, len(es)+1)
		for d := range es {
			c[d] = true
		}
		edges[n] = c
	}
	ne := map[int]bool{}
	for _, d := range ds {
		if _, ok := g.deps[d]; ok {
			ne[d] = true
		}
	}
	edges[id] = ne
	for s := range g.staged {
		for _, d := range g.deps[s] {
			if d == id {
				edges[s][id] = true
			}
		}
	}
	if hasCycle(edges) {
		return ErrCycle
	}
	g.edges = edges
	g.deps[id] = ds
	if len(ne) == len(ds) {
		g.activate(id)
	} else {
		g.staged[id] = true
	}
	for s := range g.staged {
		if len(g.edges[s]) == len(g.deps[s]) {
			g.activate(s)
		}
	}
	return nil
}

// activate 将依赖已登记齐的事务移出暂存，并按未回放依赖数入度。
func (g *Graph) activate(id int) {
	delete(g.staged, id)
	n := 0
	for _, d := range g.deps[id] {
		if !g.replayed[d] {
			n++
		}
		g.dependents[d] = append(g.dependents[d], id)
	}
	g.indeg[id] = n
	if n == 0 {
		heap.Push(&g.ready, id)
	}
}

// Peek 返回当前可回放事务中的最小 id。
func (g *Graph) Peek() (int, bool) {
	if len(g.ready) == 0 {
		return 0, false
	}
	return g.ready[0], true
}

// MarkReplayed 弹出队首事务并递减其依赖者的入度；调用方须先 Peek 得到 id。
func (g *Graph) MarkReplayed(id int) {
	heap.Pop(&g.ready)
	g.replayed[id] = true
	for _, s := range g.dependents[id] {
		g.indeg[s]--
		if g.indeg[s] == 0 {
			heap.Push(&g.ready, s)
		}
	}
}

// Staged 返回暂存事务 id，升序。
func (g *Graph) Staged() []int {
	out := make([]int, 0, len(g.staged))
	for s := range g.staged {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// hasCycle 对边集做三色 DFS 查环。
func hasCycle(edges map[int]map[int]bool) bool {
	color := make(map[int]int, len(edges))
	var visit func(n int) bool
	visit = func(n int) bool {
		color[n] = 1
		for m := range edges[n] {
			if color[m] == 1 || color[m] == 0 && visit(m) {
				return true
			}
		}
		color[n] = 2
		return false
	}
	for n := range edges {
		if color[n] == 0 && visit(n) {
			return true
		}
	}
	return false
}
