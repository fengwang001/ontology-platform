// Package check 提供并查集的朴素 BFS 参照实现，供测试交叉验证。
package check

import "ontology/uf"

// Ref 朴素参照：保存所有边，每次 Union 后用 BFS 重建连通分量。
type Ref struct {
	n     int
	edges [][2]int
	comp  []int
	count int
}

// NewRef 创建 n 个独立元素的参照结构。
func NewRef(n int) *Ref {
	r := &Ref{n: n, edges: make([][2]int, 0), comp: make([]int, n), count: n}
	for i := range r.comp {
		r.comp[i] = i
	}
	return r
}

// Union 合并两个分量并 BFS 重建标签，返回是否真正合并。
func (r *Ref) Union(x, y int) bool {
	if r.comp[x] == r.comp[y] {
		return false
	}
	r.edges = append(r.edges, [2]int{x, y})
	r.rebuild()
	return true
}

func (r *Ref) rebuild() {
	adj := make([][]int, r.n)
	for _, e := range r.edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}
	for i := range r.comp {
		r.comp[i] = -1
	}
	r.count = 0
	for s := 0; s < r.n; s++ {
		if r.comp[s] != -1 {
			continue
		}
		r.comp[s] = r.count
		queue := []int{s}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, w := range adj[v] {
				if r.comp[w] == -1 {
					r.comp[w] = r.count
					queue = append(queue, w)
				}
			}
		}
		r.count++
	}
}

// Connected 报告 x、y 是否同分量。
func (r *Ref) Connected(x, y int) bool { return r.comp[x] == r.comp[y] }

// Count 返回真实连通分量数。
func (r *Ref) Count() int { return r.count }

// 编译期保证依赖方向：check -> uf。
var _ = uf.ErrBadIndex
