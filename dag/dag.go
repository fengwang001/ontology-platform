// Package dag 维护算子节点与有向边，提供拓扑序、环检测与按拓扑序单遍的最长路 DP；不依赖其他包。
package dag

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误：调用方可用 errors.Is 判定。
var ErrEmptyName = errors.New("dag: operator name must be non-empty")
var ErrNegativeLat = errors.New("dag: latency must be >= 0")
var ErrDuplicateNode = errors.New("dag: operator already exists")
var ErrUnknownNode = errors.New("dag: link references unknown operator")
var ErrCycle = errors.New("dag: link would create a cycle")
var ErrBadTopology = errors.New("dag: graph must have exactly one source and one sink")

// Solution 是一次最长路计算的结果。
type Solution struct {
	Topo         []string          // 拓扑序
	Dist         map[string]int64  // 源→各节点最长路径（含自身 lat）
	Pred         map[string]string // 取得该 Dist 的直接前驱（源无前驱）
	Source, Sink string
	EndToEnd     int64
}

type Graph struct {
	mu         sync.Mutex
	nodes      map[string]int64 // name -> lat
	edges      map[string]map[string]struct{}
	relaxCount int // 非导出：最近一次 Solve 的边松弛考察次数；不进公开接口，仅包内测试可读
}

func New() *Graph { return &Graph{nodes: map[string]int64{}, edges: map[string]map[string]struct{}{}} }

// Add 加入算子；空名 / 负延迟 / 重名都被整体拒绝且不留痕。
func (g *Graph) Add(name string, lat int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if name == "" {
		return ErrEmptyName
	}
	if lat < 0 {
		return fmt.Errorf("%w: %s lat=%d", ErrNegativeLat, name, lat)
	}
	if _, ok := g.nodes[name]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, name)
	}
	g.nodes[name], g.edges[name] = lat, map[string]struct{}{}
	return nil
}
func (g *Graph) Link(from, to string) error {
	// 加一条有向边；未知节点或形成环（含自环）时整体拒绝且不留痕。
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: from=%s", ErrUnknownNode, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: to=%s", ErrUnknownNode, to)
	}
	if from == to || g.reaches(to, from) {
		return fmt.Errorf("%w: %s->%s", ErrCycle, from, to)
	}
	g.edges[from][to] = struct{}{}
	return nil
}
func (g *Graph) reaches(s, t string) bool { // 沿现有边 s 能否到达 t（调用时已持锁）
	seen, stack := map[string]bool{}, []string{s}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if v == t {
			return true
		}
		if !seen[v] {
			seen[v] = true
			for w := range g.edges[v] {
				stack = append(stack, w)
			}
		}
	}
	return false
}
func (g *Graph) Snapshot() (map[string]int64, map[string][]string) { // 持锁一次拷贝
	g.mu.Lock()
	defer g.mu.Unlock()
	lat, succ := map[string]int64{}, map[string][]string{}
	for n, l := range g.nodes {
		lat[n] = l
	}
	for n, tos := range g.edges {
		for to := range tos {
			succ[n] = append(succ[n], to)
		}
	}
	return lat, succ
}

// Solve 校验恰有一源一汇，按拓扑序单遍求最长路并记录边松弛次数；只读，不触碰节点与边。
func (g *Graph) Solve() (*Solution, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	indeg, succ := map[string]int{}, map[string][]string{}
	for n, tos := range g.edges { // Add 保证每个节点都有 edges 集合键（可能为空）
		for to := range tos {
			succ[n] = append(succ[n], to)
			indeg[to]++
		}
	}
	var sources, sinks []string
	for n := range g.nodes {
		if indeg[n] == 0 {
			sources = append(sources, n)
		}
		if len(succ[n]) == 0 {
			sinks = append(sinks, n)
		}
	}
	if len(sources) != 1 || len(sinks) != 1 {
		return nil, fmt.Errorf("%w: %d source(s), %d sink(s)", ErrBadTopology, len(sources), len(sinks))
	}
	source, sink := sources[0], sinks[0]
	topo, queue := make([]string, 0, len(g.nodes)), []string{source} // Kahn 拓扑序
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		topo = append(topo, v)
		for _, w := range succ[v] {
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	// 最长路 DP：dist(v)=max(dist(u)+lat(v))，每条边恰好松弛一次。
	dist := map[string]int64{source: g.nodes[source]}
	pred := map[string]string{}
	relax := 0
	for _, v := range topo {
		for _, w := range succ[v] {
			relax++
			if old, ok := dist[w]; !ok || dist[v]+g.nodes[w] > old { // !ok：区分「未设置」与距离 0
				dist[w], pred[w] = dist[v]+g.nodes[w], v
			}
		}
	}
	g.relaxCount = relax
	return &Solution{topo, dist, pred, source, sink, dist[sink]}, nil
}
