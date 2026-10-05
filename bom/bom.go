// Package bom 维护物料清单的父子关系、成环检测与低层码。
package bom

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid  = errors.New("bom: invalid parameter")
	ErrConflict = errors.New("bom: duplicate parent-child relation")
	ErrCycle    = errors.New("bom: relation would create a cycle")
)

const (
	maxPer   = int64(10_000)
	maxScrap = int64(999)
)

// Edge 是一条父子关系：每件 Parent 耗用 Per 件 Child，损耗率 Scrap 千分数。
type Edge struct {
	Parent string
	Child  string
	Per    int64
	Scrap  int64
}

type pair struct{ parent, child string }

// Graph 是物料清单图，并发安全；version 只随被接受的变更递增。
type Graph struct {
	mu       sync.RWMutex
	edges    map[pair]Edge
	byChild  map[string][]Edge
	byParent map[string][]Edge
	version  int64
}

func New() *Graph {
	return &Graph{
		edges:    make(map[pair]Edge),
		byChild:  make(map[string][]Edge),
		byParent: make(map[string][]Edge),
	}
}

func validName(name string) bool {
	return len(name) >= 1 && len(name) <= 32
}

func (g *Graph) AddComponent(parent, child string, per, scrap int64) error {
	if !validName(parent) || !validName(child) ||
		per < 1 || per > maxPer || scrap < 0 || scrap > maxScrap {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.edges[pair{parent, child}]; ok {
		return ErrConflict
	}
	if parent == child || g.reaches(child, parent) {
		return ErrCycle
	}
	e := Edge{Parent: parent, Child: child, Per: per, Scrap: scrap}
	g.edges[pair{parent, child}] = e
	g.byChild[child] = append(g.byChild[child], e)
	g.byParent[parent] = append(g.byParent[parent], e)
	g.version++
	return nil
}

// reaches 报告从 from 沿 parent→child 方向能否到达 target，调用方须持锁。
func (g *Graph) reaches(from, target string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == target {
			return true
		}
		for _, e := range g.byParent[n] {
			if !seen[e.Child] {
				seen[e.Child] = true
				stack = append(stack, e.Child)
			}
		}
	}
	return false
}

// Parents 返回 child 的全部父边，按父项字节序。
func (g *Graph) Parents(child string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := append([]Edge(nil), g.byChild[child]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Parent < out[j].Parent })
	return out
}

// LowCodes 返回图中全部物料的低层码：从任一无父物料到该物料的最长路径
// 边数，无父物料为 0。不在图中的物料由调用方按 0 处理。
func (g *Graph) LowCodes() map[string]int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	codes := make(map[string]int)
	var visit func(n string) int
	visit = func(n string) int {
		if c, ok := codes[n]; ok {
			return c
		}
		best := 0
		for _, e := range g.byChild[n] {
			if c := visit(e.Parent) + 1; c > best {
				best = c
			}
		}
		codes[n] = best
		return best
	}
	for p := range g.edges {
		visit(p.parent)
		visit(p.child)
	}
	return codes
}

func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// Version 返回被接受变更的次数。
func (g *Graph) Version() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.version
}
