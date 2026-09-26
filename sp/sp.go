// Package sp 最短路与负环检测：超级源建边、队列驱动（SPFA）求点态最大解。依赖 dc。
package sp

import (
	"errors"
	"math"

	"ontology/dc"
)

// ErrInfeasible 图中存在负权环，系统不可行。
var ErrInfeasible = errors.New("sp: negative-weight cycle, system infeasible")

const inf = math.MaxInt64 / 4

type edge struct {
	to int
	w  int64
}

// Solver 队列驱动最短路求解器。sweeps 记录整表全量松弛趟数（非导出，恒 0：
// 本实现只松弛活跃节点，从不整表重扫）；ops 记录成功松弛次数。
type Solver struct {
	adj    [][]edge
	sweeps int
	ops    int
}

// NewSolver 以超级源 src=n（x_s=0，边 src->i 权 0 即 x_i<=0）加全部约束建图。
func NewSolver(n int, cs []dc.Constraint) *Solver {
	s := &Solver{adj: make([][]edge, n+1)}
	for i := 0; i < n; i++ {
		s.adj[n] = append(s.adj[n], edge{i, 0})
	}
	for _, c := range cs {
		s.adj[c.U] = append(s.adj[c.U], edge{c.V, c.W})
	}
	return s
}

// run 队列驱动松弛：只有距离被压低的节点才入队继续传播，不做整表全量松弛。
// cnt 记录当前最短路经过的边数；达到顶点总数 n 时路径含环且环权为负，整体失败。
func (s *Solver) run() ([]int64, error) {
	n := len(s.adj)
	dist := make([]int64, n)
	for i := range dist {
		dist[i] = inf
	}
	src := n - 1
	dist[src] = 0
	inQ := make([]bool, n)
	cnt := make([]int, n)
	q := []int{src}
	inQ[src] = true
	for len(q) > 0 {
		u := q[0]
		q = q[1:]
		inQ[u] = false
		for _, e := range s.adj[u] {
			d := dist[u] + e.w
			if d >= dist[e.to] {
				continue
			}
			dist[e.to] = d
			s.ops++
			if cnt[e.to] = cnt[u] + 1; cnt[e.to] >= n {
				return nil, ErrInfeasible
			}
			if !inQ[e.to] {
				inQ[e.to] = true
				q = append(q, e.to)
			}
		}
	}
	return dist[:src], nil
}

// Solve 返回点态最大可行赋值（各变量=超级源最短路）；含负环时返回 (nil, ErrInfeasible)。
func Solve(n int, cs []dc.Constraint) ([]int64, error) {
	return NewSolver(n, cs).run()
}
