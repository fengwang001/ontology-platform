// Package traverse 提供对象图的遍历序。
//
// 选 BFS 而非 DFS：BFS 的首次到达深度 == 最短距离，使 bounded 包的
// MaxDepth 精确等于「最短距离 <= d」，且与「每节点只输出一次」的去重
// 天然兼容；DFS 的深度是路径依赖的，截断语义会随遍历顺序漂移。
package traverse

import (
	"fmt"

	"ontology/graph"
)

// Direction 是遍历方向。
type Direction int

const (
	Out  Direction = iota // 沿出边
	In                    // 沿入边
	Both                  // 出边 + 入边
)

// Valid 报告 dir 是否为合法方向。
func (d Direction) Valid() bool { return d == Out || d == In || d == Both }

func neighbors(g *graph.Graph, n string, dir Direction) []string {
	switch dir {
	case Out:
		return g.Out(n)
	case In:
		return g.In(n)
	default: // Both：出 ∪ 入，同一节点内去重
		out := g.Out(n)
		seen := make(map[string]bool, len(out))
		merged := make([]string, 0, len(out)+len(g.In(n)))
		for _, m := range out {
			seen[m] = true
			merged = append(merged, m)
		}
		for _, m := range g.In(n) {
			if !seen[m] {
				merged = append(merged, m)
			}
		}
		return merged
	}
}

// Walk 从 start 出发按 dir 做 BFS，返回节点访问序列。
// 每个节点只出现一次（按首次到达顺序）。start 不存在或 dir 非法时报错。
func Walk(g *graph.Graph, start string, dir Direction) ([]string, error) {
	if !dir.Valid() {
		return nil, fmt.Errorf("traverse: invalid direction %d", dir)
	}
	if !g.Has(start) {
		return nil, fmt.Errorf("traverse: start node %q does not exist", start)
	}
	visited := map[string]bool{start: true}
	queue := []string{start}
	var order []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range neighbors(g, n, dir) {
			if !visited[m] {
				visited[m] = true
				queue = append(queue, m)
			}
		}
	}
	return order, nil
}
