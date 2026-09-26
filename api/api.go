// Package api 是对外门面：New、选举操作包装与 SelfCheck。依赖 elect。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/elect"
)

// ErrClusterSize 集群规模非法（必须为正奇数）。
var ErrClusterSize = errors.New("api: cluster size must be a positive odd number")

// API 包装一个选举集群。
type API struct{ c *elect.Cluster }

// New 创建 n 个节点的集群，n 必须为正奇数。
func New(n int) (*API, error) {
	if n <= 0 || n%2 == 0 {
		return nil, ErrClusterSize
	}
	return &API{c: elect.New(n)}, nil
}

// StartElection 节点 node 成为候选。
func (a *API) StartElection(node int) error { return a.c.StartElection(node) }

// RequestVote 请求节点 node 投票给候选 cand（任期 t），返回是否同意。
func (a *API) RequestVote(node, cand, t int) (bool, error) {
	return a.c.RequestVote(node, cand, t)
}

// Winner 返回得票达到多数派的候选，没有则返回 -1。
func (a *API) Winner() int { return a.c.Winner() }

// Snapshot 返回全部节点 term 与 votedFor 的副本。
func (a *API) Snapshot() (terms, votedFor []int) { return a.c.State() }

// step 是自检序列的一步：start 为真表示 StartElection(node)，否则 RequestVote(node, cand, t)。
type step struct {
	start         bool
	node, cand, t int
}

// SelfCheck 在内部全新实例上执行内置操作序列，逐步核验四条不变量：
// 1) Winner 与朴素重算一致；2) 同任期不改票；3) 任期单调；4) 被拒操作不留痕。
// 同时确认三类哨兵错误互不相同且都被触发。全部通过返回 nil。
func (a *API) SelfCheck() error {
	seq := []step{
		{start: true, node: 0},
		{node: 1, cand: 0, t: 1},  // 同意
		{node: 2, cand: 0, t: 1},  // 同意，0 达多数派
		{node: 2, cand: 1, t: 1},  // 同任期已投别人：拒绝
		{node: 0, cand: 2, t: 0},  // 过期任期：拒绝
		{node: 9, cand: 0, t: 1},  // ErrNodeOutOfRange
		{node: 1, cand: 0, t: -1}, // ErrBadTerm
		{node: 1, cand: 1, t: 5},  // ErrSelfVote
		{start: true, node: 1},
		{start: true, node: 2},
		{node: 0, cand: 2, t: 3}, // 更高任期改投，2 达多数派
		{node: 2, cand: 1, t: 2}, // 同任期已投自己：拒绝
	}
	c := elect.New(3)
	prevT, prevV := c.State()
	seenErrs := map[error]bool{}
	for i, s := range seq {
		var err error
		if s.start {
			err = c.StartElection(s.node)
		} else {
			_, err = c.RequestVote(s.node, s.cand, s.t)
		}
		nowT, nowV := c.State()
		if err != nil { // 不变量 4：失败不留痕
			if !reflect.DeepEqual(prevT, nowT) || !reflect.DeepEqual(prevV, nowV) {
				return fmt.Errorf("selfcheck step %d: rejected op mutated state", i)
			}
			seenErrs[err] = true
		}
		for j := range nowT {
			if nowT[j] < prevT[j] { // 不变量 3：任期单调
				return fmt.Errorf("selfcheck step %d: term of node %d regressed", i, j)
			}
			if nowT[j] == prevT[j] && prevV[j] != -1 && nowV[j] != prevV[j] { // 不变量 2
				return fmt.Errorf("selfcheck step %d: node %d changed vote within term", i, j)
			}
		}
		if w, nw := c.Winner(), naiveWinner(nowV, 2); w != nw { // 不变量 1
			return fmt.Errorf("selfcheck step %d: winner %d != naive %d", i, w, nw)
		}
		prevT, prevV = nowT, nowV
	}
	if len(seenErrs) != 3 {
		return fmt.Errorf("selfcheck: want 3 distinct sentinel errors, got %d", len(seenErrs))
	}
	return nil
}

// naiveWinner 朴素重算：逐个扫描全部节点统计得票，取达到多数派的候选。
func naiveWinner(votedFor []int, maj int) int {
	cnt := map[int]int{}
	for _, v := range votedFor {
		if v >= 0 {
			cnt[v]++
		}
	}
	for cand, n := range cnt {
		if n >= maj {
			return cand // 计票总和 <= n，多数派至多一个
		}
	}
	return -1
}
