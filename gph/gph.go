// Package gph 维护事务依赖图：登记/暂存、激活、环检测与入度（未回放依赖数）。
// 不依赖任何其他包。本包不做并发控制，由上层加锁。
package gph

import (
	"errors"
	"sort"
)

var (
	ErrInvalidID = errors.New("gph: invalid id")
	ErrSelfDep   = errors.New("gph: self dependency")
	ErrDuplicate = errors.New("gph: duplicate commit")
	ErrCycle     = errors.New("gph: dependency cycle")
	ErrFull      = errors.New("gph: max transactions reached")
)

// Graph 是事务依赖图。deps 记录所有已登记事务声明的依赖（含暂存者），
// 环检测在该声明图上进行；active 中的事务才进入回放图并维护入度。
type Graph struct {
	max        int
	deps       map[int]map[int]bool // 已登记事务 -> 声明的依赖集合
	staged     map[int]bool         // 依赖未登记齐、等待激活
	active     map[int]bool         // 已进入回放图
	indeg      map[int]int          // active 事务的未回放依赖数
	dependents map[int]map[int]bool // 反向声明边：d -> 依赖 d 的已登记事务
	replayed   map[int]bool
}

func New(maxTxns int) *Graph {
	return &Graph{
		max:        maxTxns,
		deps:       map[int]map[int]bool{},
		staged:     map[int]bool{},
		active:     map[int]bool{},
		indeg:      map[int]int{},
		dependents: map[int]map[int]bool{},
		replayed:   map[int]bool{},
	}
}

// Commit 登记事务 id 及其依赖。全部校验先于任何变更，失败不留痕。
// 返回本次登记（含级联激活）后新变为可回放（入度 0）的事务 id。
func (g *Graph) Commit(id int, deps []int) ([]int, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}
	depSet := map[int]bool{}
	for _, d := range deps {
		if d <= 0 {
			return nil, ErrInvalidID
		}
		if d == id {
			return nil, ErrSelfDep
		}
		depSet[d] = true
	}
	if _, ok := g.deps[id]; ok {
		return nil, ErrDuplicate
	}
	if len(g.deps) >= g.max {
		return nil, ErrFull
	}
	if g.createsCycle(id, depSet) {
		return nil, ErrCycle
	}
	g.deps[id] = depSet
	for d := range depSet {
		if g.dependents[d] == nil {
			g.dependents[d] = map[int]bool{}
		}
		g.dependents[d][id] = true
	}
	activated := map[int]bool{}
	if g.registeredAll(depSet) {
		g.activate(id)
		activated[id] = true
	} else {
		g.staged[id] = true
	}
	// 级联激活：本事务可能正是某些暂存事务缺的最后一个依赖。
	for {
		progressed := false
		for s := range g.staged {
			if g.registeredAll(g.deps[s]) {
				g.activate(s)
				activated[s] = true
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	// 只报告本次新激活且入度 0 者，调用方据此增量维护就绪集。
	var ready []int
	for a := range activated {
		if g.indeg[a] == 0 && !g.replayed[a] {
			ready = append(ready, a)
		}
	}
	return ready, nil
}

// MarkReplayed 标记 id 已回放，递减其依赖者的入度，返回新变为可回放的事务。
func (g *Graph) MarkReplayed(id int) []int {
	g.replayed[id] = true
	var ready []int
	for x := range g.dependents[id] {
		if !g.active[x] || g.replayed[x] {
			continue
		}
		g.indeg[x]--
		if g.indeg[x] == 0 {
			ready = append(ready, x)
		}
	}
	return ready
}

// Staged 返回当前暂存事务，按 id 升序。
func (g *Graph) Staged() []int {
	out := make([]int, 0, len(g.staged))
	for s := range g.staged {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

func (g *Graph) registeredAll(depSet map[int]bool) bool {
	for d := range depSet {
		if _, ok := g.deps[d]; !ok {
			return false
		}
	}
	return true
}

func (g *Graph) activate(id int) {
	delete(g.staged, id)
	g.active[id] = true
	n := 0
	for d := range g.deps[id] {
		if !g.replayed[d] {
			n++
		}
	}
	g.indeg[id] = n
}

// createsCycle 判断「新增 id -> depSet 的边」是否成环：环必经过 id，
// 故只需检查某个依赖 d 能否沿声明边到达 id。
func (g *Graph) createsCycle(id int, depSet map[int]bool) bool {
	for d := range depSet {
		if g.reaches(d, id) {
			return true
		}
	}
	return false
}

func (g *Graph) reaches(from, to int) bool {
	seen := map[int]bool{}
	stack := []int{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		for next := range g.deps[cur] {
			stack = append(stack, next)
		}
	}
	return false
}
