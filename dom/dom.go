// Package dom 在 dgraph 上计算支配树并回答支配查询。
package dom

import (
	"sync/atomic"

	"ontology/dgraph"
)

// Dom 保存一次 Compute 的结果：idom 数组与支配树的 DFS 进出区间。
type Dom struct {
	n       int
	idom    []int // 不可达节点为 -1；idom[0]==0
	tin     []int // 不可达节点为 -1
	tout    []int
	checked atomic.Int64 // 最近一次 Dominates 查询检查的节点个数（非导出）
}

// Compute 用 Cooper 等人的迭代交点算法求每个可达节点的 idom，
// 再在支配树上做 DFS 得到进出区间。
func Compute(g *dgraph.Graph) *Dom {
	n := g.N()
	d := &Dom{n: n, idom: make([]int, n), tin: make([]int, n), tout: make([]int, n)}
	for i := 0; i < n; i++ {
		d.idom[i], d.tin[i] = -1, -1
	}
	rpo := reversePostOrder(g)
	rpoNum := make([]int, n)
	for i, v := range rpo {
		rpoNum[v] = i
	}
	preds := g.Preds()
	d.idom[0] = 0
	intersect := func(a, b int) int {
		for a != b {
			for rpoNum[a] > rpoNum[b] {
				a = d.idom[a]
			}
			for rpoNum[b] > rpoNum[a] {
				b = d.idom[b]
			}
		}
		return a
	}
	for changed := true; changed; {
		changed = false
		for _, v := range rpo[1:] {
			ni := -1
			for _, p := range preds[v] {
				if d.idom[p] < 0 {
					continue
				}
				if ni < 0 {
					ni = p
				} else {
					ni = intersect(p, ni)
				}
			}
			if ni != d.idom[v] {
				d.idom[v] = ni
				changed = true
			}
		}
	}
	d.intervals()
	return d
}

// reversePostOrder 返回从 0 出发 DFS 的逆后序（只含可达节点）。
func reversePostOrder(g *dgraph.Graph) []int {
	n := g.N()
	vis := make([]bool, n)
	var post []int
	type frame struct{ v, i int }
	st := []frame{{0, 0}}
	vis[0] = true
	for len(st) > 0 {
		f := &st[len(st)-1]
		succ := g.Succ(f.v)
		if f.i < len(succ) {
			w := succ[f.i]
			f.i++
			if !vis[w] {
				vis[w] = true
				st = append(st, frame{w, 0})
			}
			continue
		}
		post = append(post, f.v)
		st = st[:len(st)-1]
	}
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}

// intervals 在支配树上做 DFS，给每个可达节点分配进出时间戳。
func (d *Dom) intervals() {
	children := make([][]int, d.n)
	for v := 1; v < d.n; v++ {
		if d.idom[v] >= 0 {
			p := d.idom[v]
			children[p] = append(children[p], v)
		}
	}
	timer := 0
	type ev struct {
		v    int
		exit bool
	}
	st := []ev{{0, false}}
	for len(st) > 0 {
		e := st[len(st)-1]
		st = st[:len(st)-1]
		if e.exit {
			d.tout[e.v] = timer
			timer++
			continue
		}
		d.tin[e.v] = timer
		timer++
		st = append(st, ev{e.v, true})
		for i := len(children[e.v]) - 1; i >= 0; i-- {
			st = append(st, ev{children[e.v][i], false})
		}
	}
}

// IDom 返回 v 的直接支配者；不可达或越界返回 -1。
func (d *Dom) IDom(v int) int {
	if v < 0 || v >= d.n {
		return -1
	}
	return d.idom[v]
}

// Dominates 报告 a 是否支配 b：b 不可达恒 false，不可达节点不支配任何节点。
// 用预计算的进出区间 O(1) 回答，只检查 a、b 两个节点。
func (d *Dom) Dominates(a, b int) bool {
	d.checked.Store(2)
	if a < 0 || a >= d.n || b < 0 || b >= d.n {
		return false
	}
	if d.tin[a] < 0 || d.tin[b] < 0 {
		return false
	}
	return d.tin[a] <= d.tin[b] && d.tout[b] <= d.tout[a]
}
