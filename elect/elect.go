// Package elect 维护多节点的选举状态：StartElection、RequestVote
// 与 Winner 的多数派计数。依赖 term 包。
package elect

import (
	"errors"
	"sync"

	"ontology/term"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrNodeIndex = errors.New("elect: node index out of range")
	ErrBadTerm   = errors.New("elect: negative vote term")
	ErrSelfVote  = errors.New("elect: self vote must go through StartElection")
)

// Cluster 是 n 个节点（n 为奇数）的选举状态。
type Cluster struct {
	mu        sync.Mutex
	nodes     []term.Book
	majority  int
	counts    map[int]int // 每个候选当前的得票数（增量维护）
	winner    int         // 达到多数派的候选，无则 -1
	lastReads int         // 最近一次 Winner 读取过的节点个数（非导出，不进公开接口）
}

// New 创建 n 个节点的集群，term 全 0、votedFor 全 -1。
func New(n int) *Cluster {
	if n < 1 || n%2 == 0 {
		panic("elect: n must be a positive odd number")
	}
	nodes := make([]term.Book, n)
	for i := range nodes {
		nodes[i] = term.New()
	}
	return &Cluster{
		nodes:    nodes,
		majority: n/2 + 1,
		counts:   map[int]int{},
		winner:   -1,
	}
}

// N 返回节点个数。
func (c *Cluster) N() int { return len(c.nodes) }

// Term 返回节点 i 的当前任期。
func (c *Cluster) Term(i int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[i].Term()
}

// VotedFor 返回节点 i 本任期投给的候选。
func (c *Cluster) VotedFor(i int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[i].VotedFor()
}

// StartElection 使节点 id 成为候选：任期加 1、投给自己。
func (c *Cluster) StartElection(id int) error {
	if err := c.checkNode(id); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.nodes[id].VotedFor()
	c.nodes[id].StartElection(id)
	c.move(old, id)
	return nil
}

// RequestVote 请求节点 i 把票投给候选 cand（任期 t）。
// 拒绝（任期过期或本任期已投他人）返回 nil 且状态不变；
// 参数非法返回可判定的哨兵错误且状态不变。
func (c *Cluster) RequestVote(i, cand, t int) error {
	if err := c.checkNode(i); err != nil {
		return err
	}
	if err := c.checkNode(cand); err != nil {
		return err
	}
	if t < 0 {
		return ErrBadTerm
	}
	if i == cand {
		return ErrSelfVote
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.nodes[i].VotedFor()
	if !c.nodes[i].RequestVote(cand, t) {
		return nil // 拒绝：不落账
	}
	if old != cand {
		c.move(old, cand)
	}
	return nil
}

// Winner 返回达到多数派的候选，无则 -1。
// 直接读增量维护的结果，不扫描节点：读取节点个数恒为 0。
func (c *Cluster) Winner() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastReads = 0
	return c.winner
}

// WinnerReadBounded 报告最近一次 Winner 的节点读取数是否不超过小常数。
// 只暴露是否越界的判定，不暴露计数器数值本身。
func (c *Cluster) WinnerReadBounded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReads <= 1
}

func (c *Cluster) checkNode(i int) error {
	if i < 0 || i >= len(c.nodes) {
		return ErrNodeIndex
	}
	return nil
}

// move 把一票从 old 候选移到 new 候选，并增量维护 winner。
// 调用方必须持锁；old 为 -1 表示此前未投。
func (c *Cluster) move(old, new int) {
	if old >= 0 {
		c.counts[old]--
		if c.winner == old && c.counts[old] < c.majority {
			c.winner = -1
		}
	}
	c.counts[new]++
	if c.counts[new] >= c.majority {
		c.winner = new
	}
}
