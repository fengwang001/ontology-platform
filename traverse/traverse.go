// Package traverse 提供对象图的遍历序。
// 选 BFS：深度即最短距离，与 bounded 的 MaxDepth 语义对齐（DESIGN.md）。
package traverse

import (
	"errors"
	"sort"

	"ontology/graph"
)

// Dir 是遍历方向。
type Dir int

const (
	Out  Dir = iota // 沿出边
	In              // 沿入边
	Both            // 出边 ∪ 入边
)

var ErrInvalidDir = errors.New("traverse: invalid direction")

func (d Dir) Valid() bool { return d >= Out && d <= Both }

func (d Dir) String() string {
	switch d {
	case Out:
		return "out"
	case In:
		return "in"
	case Both:
		return "both"
	}
	return "unknown"
}

// Neighbors 返回 node 在 dir 方向上的邻居（排序保证确定性输出）。
func Neighbors(g *graph.Graph, node string, dir Dir) []string {
	var ns []string
	switch dir {
	case Out:
		ns = g.Out(node)
	case In:
		ns = g.In(node)
	case Both:
		ns = append(g.Out(node), g.In(node)...)
	}
	sort.Strings(ns)
	return ns
}

// Walk 从 start 出发做 BFS，返回去重后的访问序列（首到即最浅深度）。
// start 不存在或 dir 非法时返回错误。
func Walk(g *graph.Graph, start string, dir Dir) ([]string, error) {
	if !dir.Valid() {
		return nil, ErrInvalidDir
	}
	if !g.Has(start) {
		return nil, graph.ErrNodeNotFound
	}
	seen := map[string]bool{start: true}
	order := []string{start}
	queue := []string{start}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range Neighbors(g, node, dir) {
			if !seen[next] {
				seen[next] = true
				order = append(order, next)
				queue = append(queue, next)
			}
		}
	}
	return order, nil
}
