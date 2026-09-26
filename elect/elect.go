// Package elect 维护多节点集群的选举状态：StartElection、RequestVote、
// Winner 的多数派计数。依赖 term 包。
package elect

import (
	"errors"
	"sync"

	"ontology/term"
)

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrNodeOutOfRange = errors.New("elect: node index out of range")
	ErrBadTerm        = errors.New("elect: negative vote term")
	ErrSelfVote       = errors.New("elect: self vote must go through StartElection")
)

// Cluster 是 n 个节点（n 为奇数）的选举状态。
type Cluster struct {
	mu    sync.Mutex
	nodes []term.Book
	votes map[int]int // 候选 -> 当前得票数（增量维护，与 nodes 始终一致）
	maj   int         // 多数派 = n/2 + 1
	reads int         // 最近一次 Winner() 读取过的节点个数（非导出，仅供包内测试观测）
}

// New 创建 n 个节点的集群。调用方保证 n 为正奇数。
func New(n int) *Cluster {
	c := &Cluster{nodes: make([]term.Book, n), votes: make(map[int]int), maj: n/2 + 1}
	for i := range c.nodes {
		c.nodes[i] = term.New()
	}
	return c
}

// Size 返回节点个数。
func (c *Cluster) Size() int { return len(c.nodes) }

// StartElection 节点 node 成为候选：任期加 1 并投给自己。
func (c *Cluster) StartElection(node int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if node < 0 || node >= len(c.nodes) {
		return ErrNodeOutOfRange
	}
	b := &c.nodes[node]
	old := b.VotedFor
	b.StartElection(node)
	c.retally(old, b.VotedFor)
	return nil
}

// RequestVote 请求节点 node 把票投给候选 cand（任期 t）。
// 返回是否同意；三类故障返回哨兵错误且状态不变。
func (c *Cluster) RequestVote(node, cand, t int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if node < 0 || node >= len(c.nodes) || cand < 0 || cand >= len(c.nodes) {
		return false, ErrNodeOutOfRange
	}
	if t < 0 {
		return false, ErrBadTerm
	}
	if cand == node {
		return false, ErrSelfVote
	}
	b := &c.nodes[node]
	old := b.VotedFor
	if !b.RequestVote(cand, t) {
		return false, nil // 正常拒绝：不是错误，状态不变
	}
	c.retally(old, b.VotedFor)
	return true, nil
}

// retally 把一票从 old 移到 now（-1 表示未投，不计数）。调用方须持锁。
func (c *Cluster) retally(old, now int) {
	if old == now {
		return
	}
	if old >= 0 {
		c.votes[old]--
	}
	if now >= 0 {
		c.votes[now]++
	}
}

// Winner 返回得票达到多数派的候选，没有则返回 -1。
// 直接读增量计票表，不扫描节点：读取节点个数恒为 0，与集群规模无关。
func (c *Cluster) Winner() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = 0
	for cand, n := range c.votes {
		if n >= c.maj {
			return cand // 计票总和 <= n，多数派至多一个
		}
	}
	return -1
}

// State 返回全部节点 term 与 votedFor 的副本。
func (c *Cluster) State() (terms, votedFor []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	terms = make([]int, len(c.nodes))
	votedFor = make([]int, len(c.nodes))
	for i, b := range c.nodes {
		terms[i], votedFor[i] = b.Term, b.VotedFor
	}
	return terms, votedFor
}
