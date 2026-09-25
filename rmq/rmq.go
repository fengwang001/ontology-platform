// Package rmq 在 seg 存储上实现区间最小值查询与单点更新。
package rmq

import (
	"sync/atomic"

	"ontology/seg"
)

// RMQ 是一棵支持 [l,r) 最小值查询与单点更新的线段树。
type RMQ struct {
	t *seg.Tree
	// lastVisits 记录最近一次 Query 访问过的树节点个数。
	// 非导出，仅供包内白盒测试核验对数复杂度，不出现在公开接口。
	// 用 atomic 保证并发只读时 -race 干净。
	lastVisits atomic.Int64
}

// Build 用 arr 建树；n 非 2 的幂时补齐叶子保持 seg.Inf（New 已保证），
// 真实叶子写入后自底向上重算内部节点。
func Build(arr []int64) *RMQ {
	t := seg.New(len(arr))
	for i, v := range arr {
		t.Nodes[t.Leaf(i)] = v
	}
	for i := t.Size - 1; i >= 1; i-- {
		t.Nodes[i] = seg.Min(t.Nodes[seg.Left(i)], t.Nodes[seg.Right(i)])
	}
	return &RMQ{t: t}
}

// N 返回真实元素个数。
func (r *RMQ) N() int { return r.t.N }

// Query 返回 [l, r) 的最小值；空区间返回 seg.Inf。
// 调用方必须保证 0<=l<=r<=N（api 层负责校验）。
// 迭代式：l、r 提升到叶子层后相向收缩，只访问与区间相交的节点。
func (r *RMQ) Query(l, rr int) int64 {
	res := seg.Inf
	visits := 0
	l += r.t.Size
	rr += r.t.Size
	for l < rr {
		if l%2 == 1 {
			res = seg.Min(res, r.t.Nodes[l])
			visits++
			l++
		}
		if rr%2 == 1 {
			rr--
			res = seg.Min(res, r.t.Nodes[rr])
			visits++
		}
		l /= 2
		rr /= 2
	}
	r.lastVisits.Store(int64(visits))
	return res
}

// Update 把下标 i 改为 v，并沿祖先一路重算到根。
// 调用方必须保证 0<=i<N。
func (r *RMQ) Update(i int, v int64) {
	p := r.t.Leaf(i)
	r.t.Nodes[p] = v
	for p = seg.Parent(p); p >= 1; p = seg.Parent(p) {
		r.t.Nodes[p] = seg.Min(r.t.Nodes[seg.Left(p)], r.t.Nodes[seg.Right(p)])
	}
}
