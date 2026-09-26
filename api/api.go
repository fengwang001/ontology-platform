// Package api 是对外入口：New、各操作的包装与 SelfCheck。依赖 elect。
package api

import (
	"fmt"
	"slices"

	"ontology/elect"
)

// Cluster 包装 elect.Cluster，提供对外操作。
type Cluster struct{ c *elect.Cluster }

// New 创建 n 个节点（n 为正奇数）的集群。
func New(n int) *Cluster { return &Cluster{c: elect.New(n)} }

// N 返回节点个数。
func (a *Cluster) N() int { return a.c.N() }

// StartElection 使节点 id 成为候选。
func (a *Cluster) StartElection(id int) error { return a.c.StartElection(id) }

// RequestVote 请求节点 i 投票给候选 cand（任期 t）。
func (a *Cluster) RequestVote(i, cand, t int) error { return a.c.RequestVote(i, cand, t) }

// Winner 返回达到多数派的候选，无则 -1。
func (a *Cluster) Winner() int { return a.c.Winner() }

// Term 返回节点 i 的当前任期。
func (a *Cluster) Term(i int) int { return a.c.Term(i) }

// VotedFor 返回节点 i 本任期投给的候选。
func (a *Cluster) VotedFor(i int) int { return a.c.VotedFor(i) }

// WinnerReadBounded 报告 Winner 的节点读取数是否不随规模增长。
func (a *Cluster) WinnerReadBounded() bool { return a.c.WinnerReadBounded() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *Cluster) SelfCheck() error {
	for _, n := range []int{3, 5, 9} {
		if err := checkInvariants(n); err != nil {
			return err
		}
	}
	return nil
}

// checkInvariants 在 n 节点集群上跑一段确定性操作序列，逐条核验不变量。
func checkInvariants(n int) error {
	c := New(n)
	terms := make([]int, n)
	votes := make([]int, n)
	cast := make(map[[2]int]int) // (节点, 任期) -> 已投候选
	for i := range votes {
		votes[i] = -1
	}
	ops := opsFor(n)
	snap := func() [][2]int {
		s := make([][2]int, n)
		for i := 0; i < n; i++ {
			s[i] = [2]int{c.Term(i), c.VotedFor(i)}
		}
		return s
	}
	for k, op := range ops {
		before := snap()
		err := op.run(c)
		after := snap()
		if err != nil { // 不变量 4：失败不留痕
			if !slices.Equal(before, after) {
				return fmt.Errorf("selfcheck: rejected op %d changed state", k)
			}
			continue
		}
		for i := 0; i < n; i++ {
			if after[i][0] < terms[i] { // 不变量 3：任期单调
				return fmt.Errorf("selfcheck: term of node %d regressed", i)
			}
			if after[i][0] != terms[i] { // 任期提升才允许改票
				delete(cast, [2]int{i, terms[i]})
			}
			if prev, ok := cast[[2]int{i, after[i][0]}]; ok && after[i][1] != prev {
				return fmt.Errorf("selfcheck: node %d revoted in term %d", i, after[i][0])
			}
			if after[i][1] != -1 {
				cast[[2]int{i, after[i][0]}] = after[i][1]
			}
			terms[i], votes[i] = after[i][0], after[i][1]
		}
		if got, want := c.Winner(), naive(c); got != want { // 不变量 1
			return fmt.Errorf("selfcheck: winner %d != naive %d", got, want)
		}
	}
	return nil
}

// naive 朴素重算：扫描全部节点统计每个候选得票，取达到多数派者。
func naive(c *Cluster) int {
	n := c.N()
	cnt := map[int]int{}
	for i := 0; i < n; i++ {
		if v := c.VotedFor(i); v != -1 {
			cnt[v]++
		}
	}
	for cand, k := range cnt {
		if k >= n/2+1 {
			return cand
		}
	}
	return -1
}

type op struct {
	kind       int // 0=StartElection, 1=RequestVote
	i, cand, t int
}

func (o op) run(c *Cluster) error {
	if o.kind == 0 {
		return c.StartElection(o.i)
	}
	return c.RequestVote(o.i, o.cand, o.t)
}

// opsFor 生成覆盖同意/拒绝/三类非法参数的确定性操作序列。
func opsFor(n int) []op {
	ops := []op{{kind: 0, i: 0}}
	for i := 1; i < n; i++ {
		ops = append(ops, op{kind: 1, i: i, cand: 0, t: 1})
	}
	ops = append(ops,
		op{kind: 1, i: 1, cand: 1, t: 1},     // 同任期改投他人：拒绝
		op{kind: 1, i: 0, cand: 2 % n, t: 0}, // 过期任期：拒绝
		op{kind: 0, i: -1},                   // 下标越界
		op{kind: 0, i: n},                    // 下标越界
		op{kind: 1, i: 0, cand: n, t: 2},     // 候选越界
		op{kind: 1, i: 0, cand: 1, t: -1},    // 任期非法
		op{kind: 1, i: 1, cand: 1, t: 2},     // 自投票
		op{kind: 0, i: 1},                    // 节点1成为候选，任期提升
		op{kind: 1, i: 2 % n, cand: 1, t: 2}, // 节点1在任期2拉票
		op{kind: 1, i: 0, cand: 0, t: 2},     // 自投票（候选=目标）
	)
	return ops
}
