// Package graph 构建任务依赖图，提供环检测与拓扑分层。
package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrCycle 表示图中存在环；ErrUnknownTask 表示引用了未注册的任务。
var (
	ErrCycle       = errors.New("graph contains a cycle")
	ErrUnknownTask = errors.New("unknown task")
)

// CycleError 携带一条真实闭合的环路径（每条边都在输入中，首尾相同）。
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string { return "cycle detected: " + strings.Join(e.Path, " -> ") }

func (e *CycleError) Unwrap() error { return ErrCycle }

// Graph 是任务依赖图：边 from->to 表示 to 依赖 from，from 先执行。
type Graph struct {
	deps       map[string]map[string]bool
	dependents map[string]map[string]bool
}

func New() *Graph {
	return &Graph{deps: map[string]map[string]bool{}, dependents: map[string]map[string]bool{}}
}

// Add 注册一个任务，重复注册幂等。
func (g *Graph) Add(id string) {
	if _, ok := g.deps[id]; !ok {
		g.deps[id] = map[string]bool{}
		g.dependents[id] = map[string]bool{}
	}
}

// Edge 声明 to 依赖 from。两端任务必须已注册，重复加边幂等。
func (g *Graph) Edge(from, to string) error {
	if _, ok := g.deps[from]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, from)
	}
	if _, ok := g.deps[to]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, to)
	}
	g.deps[to][from] = true
	g.dependents[from][to] = true
	return nil
}

// HasEdge 报告输入中是否存在 from->to 这条边。
func (g *Graph) HasEdge(from, to string) bool { return g.deps[to][from] }

// Tasks 返回按字典序排序的全部任务 ID。
func (g *Graph) Tasks() []string {
	ids := make([]string, 0, len(g.deps))
	for id := range g.deps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Deps 返回 id 的直接依赖（升序）。
func (g *Graph) Deps(id string) []string { return sortedKeys(g.deps[id]) }

// Dependents 返回直接依赖 id 的任务（升序）。
func (g *Graph) Dependents(id string) []string { return sortedKeys(g.dependents[id]) }

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Layers 用迭代式 Kahn 算法做拓扑分层，同层内按 ID 升序。
// 有环时返回携带真实环路径的 *CycleError。
func (g *Graph) Layers() ([][]string, error) {
	indeg := map[string]int{}
	var cur []string
	for _, id := range g.Tasks() {
		indeg[id] = len(g.deps[id])
		if indeg[id] == 0 {
			cur = append(cur, id)
		}
	}
	done := 0
	var layers [][]string
	for len(cur) > 0 {
		layers = append(layers, cur)
		done += len(cur)
		var next []string
		for _, id := range cur {
			for _, dep := range g.Dependents(id) {
				indeg[dep]--
				if indeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		sort.Strings(next)
		cur = next
	}
	if done != len(indeg) {
		return nil, &CycleError{Path: g.findCycle(indeg)}
	}
	return layers, nil
}

// findCycle 在 Kahn 残余子图（入度 > 0）中沿前驱边走，直到撞到已访问节点，
// 截取重复段并闭合，得到每条边都真实存在的环路径。
func (g *Graph) findCycle(indeg map[string]int) []string {
	start := ""
	for _, id := range g.Tasks() {
		if indeg[id] > 0 {
			start = id
			break
		}
	}
	seen := map[string]int{}
	var path []string
	cur := start
	for {
		if idx, ok := seen[cur]; ok {
			rev := path[idx:]
			cyc := make([]string, 0, len(rev)+1)
			for i := len(rev) - 1; i >= 0; i-- {
				cyc = append(cyc, rev[i])
			}
			return append(cyc, cyc[0])
		}
		seen[cur] = len(path)
		path = append(path, cur)
		for _, pre := range g.Deps(cur) {
			if indeg[pre] > 0 {
				cur = pre
				break
			}
		}
	}
}
