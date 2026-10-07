// Package index 提供单个主键维度上的双时态索引与历史重建能力。
//
// 索引基于按优先级堆序的持久化 treap：每次提交在旧根的基础上路径复制出
// 新根，roots[k] 即"前 k 个版本提交后"的业务时间起点有序集合快照。
// 查询先在系统时间数组上二分定位可见前缀 k，再在 roots[k] 上做前驱查找，
// 单次查询访问节点数为 O(log n)，与主键历史版本总数、对象类型下实例总数
// 均无关（每个主键一棵独立的 Chain）。
package index

import "sort"

// node 是持久化 treap 的不可变节点。key 为业务时间起点，seq 为截至当前
// 快照该业务起点上最新（系统时间最大）的版本号。
type node struct {
	key         int64
	prio        uint64
	seq         int64
	left, right *node
}

// Chain 是单个主键的双时态索引。版本必须按提交顺序（系统时间严格递增）
// 依次 Add。
type Chain struct {
	sys     []int64 // sys[i-1] 为第 i 个提交版本的系统时间，严格递增
	roots   []*node // roots[k] 为前 k 个版本提交后的 treap 根，roots[0] == nil
	visited int     // 最近一次 Query 访问的节点数，用于复杂度证明
}

// NewChain 创建空索引。
func NewChain() *Chain {
	return &Chain{roots: []*node{nil}}
}

// Add 提交一个新版本。sys 必须严格大于此前所有版本的系统时间；
// bizStart 为该版本声明的业务时间起点；seq 为主键内单调递增的版本号。
func (c *Chain) Add(bizStart, sys, seq int64) {
	if n := len(c.sys); n > 0 && sys <= c.sys[n-1] {
		panic("index: sys 必须严格递增")
	}
	c.sys = append(c.sys, sys)
	root := insert(c.roots[len(c.roots)-1], bizStart, seq, priority(bizStart))
	c.roots = append(c.roots, root)
}

// Query 在"系统时间不晚于 sysQ"的可见前缀内，找到业务时间区间覆盖
// bizQ 且系统时间最晚的版本，返回其版本号。不存在时返回 ok == false。
func (c *Chain) Query(sysQ, bizQ int64) (seq int64, ok bool) {
	// 可见前缀：系统时间不晚于 sysQ 的版本数。
	k := sort.Search(len(c.sys), func(i int) bool { return c.sys[i] > sysQ })
	if k == 0 {
		c.visited = 0
		return 0, false
	}
	// 在 roots[k] 上做前驱查找：业务时间区间覆盖 bizQ 的版本，
	// 其业务起点必为可见集合中 <= bizQ 的最大起点 s*；同一 s* 下
	// 系统时间最晚的版本即节点记录的 seq（后提交者覆盖先提交者）。
	var best *node
	visited := 0
	for n := c.roots[k]; n != nil; {
		visited++
		if n.key <= bizQ {
			if best == nil || n.key > best.key {
				best = n
			}
			n = n.right
		} else {
			n = n.left
		}
	}
	c.visited = visited
	if best == nil {
		return 0, false
	}
	return best.seq, true
}

// Len 返回已提交版本数。
func (c *Chain) Len() int { return len(c.sys) }

// LastQueryVisited 返回最近一次 Query 访问的 treap 节点数，
// 用于以可验证方式证明查询开销为 O(log n)。
func (c *Chain) LastQueryVisited() int { return c.visited }

// priority 由业务起点确定性导出堆优先级，保证同一操作序列重放
// 得到完全相同的 treap 结构。
func priority(key int64) uint64 {
	z := uint64(key) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// insert 在持久化 treap 中插入或覆盖 key，返回新根；旧树不被修改。
func insert(n *node, key, seq int64, prio uint64) *node {
	if n == nil {
		return &node{key: key, prio: prio, seq: seq}
	}
	if key == n.key {
		c := *n
		c.seq = seq
		return &c
	}
	if key < n.key {
		l := insert(n.left, key, seq, prio)
		c := *n
		c.left = l
		if l.prio > c.prio {
			return rotateRight(&c)
		}
		return &c
	}
	r := insert(n.right, key, seq, prio)
	c := *n
	c.right = r
	if r.prio > c.prio {
		return rotateLeft(&c)
	}
	return &c
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}
