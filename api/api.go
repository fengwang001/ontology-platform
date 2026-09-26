// Package api 是 2PC 协调者状态机的对外入口。
package api

import (
	"errors"
	"fmt"

	"ontology/coord"
	"ontology/pt"
)

// 对外暴露的可判定哨兵错误。
var (
	ErrOutOfRange    = coord.ErrOutOfRange
	ErrDuplicateVote = pt.ErrDuplicateVote
	ErrNotAllVoted   = coord.ErrNotAllVoted
)

// Tx 是一个两阶段提交事务。
type Tx struct {
	c *coord.Coordinator
}

// New 创建一个有 n 个参与者的事务。
func New(n int) *Tx { return &Tx{c: coord.New(n)} }

// Vote 参与者 p 投票（yes=true 赞成）。
func (t *Tx) Vote(p int, yes bool) error { return t.c.Vote(p, yes) }

// Decide 在全部参与者投票后给出决定；未投齐返回 ErrNotAllVoted。
func (t *Tx) Decide() (coord.Decision, error) { return t.c.Decide() }

// Recover 崩溃恢复：已决定则保持，已投齐按规则决定，否则安全中止。
func (t *Tx) Recover() coord.Decision { return t.c.Recover() }

// Counts 返回当前 yes/no 票数。
func (t *Tx) Counts() (yes, no int) { return t.c.Counts() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量1+2：与朴素重算一致 & 全体一致（确定性伪随机序列对拍）
	for n := 1; n <= 8; n++ {
		tx := New(n)
		votes := make([]int, n) // 0 未投 1 yes 2 no
		seed := uint32(n*7919 + 13)
		for step := 0; step < 4*n; step++ {
			seed = seed*1664525 + 1013904223
			p := int(seed >> 16 % uint32(n))
			yes := seed>>31 == 0
			if tx.Vote(p, yes) == nil {
				if yes {
					votes[p] = 1
				} else {
					votes[p] = 2
				}
			}
		}
		for p := range votes { // 补满未投的
			if votes[p] == 0 {
				if tx.Vote(p, true) == nil {
					votes[p] = 1
				}
			}
		}
		got, err := tx.Decide()
		if err != nil {
			return fmt.Errorf("selfcheck: decide after full votes: %w", err)
		}
		want := coord.Commit
		for _, v := range votes {
			if v != 1 {
				want = coord.Abort
			}
		}
		if got != want {
			return fmt.Errorf("selfcheck: n=%d decide=%v want %v", n, got, want)
		}
	}
	// 不变量3：票不可改
	tx := New(2)
	if err := tx.Vote(0, false); err != nil {
		return fmt.Errorf("selfcheck: first vote: %w", err)
	}
	if err := tx.Vote(0, true); err == nil {
		return errors.New("selfcheck: duplicate vote accepted")
	}
	if err := tx.Vote(1, true); err != nil {
		return fmt.Errorf("selfcheck: second vote: %w", err)
	}
	if d, _ := tx.Decide(); d != coord.Abort {
		return fmt.Errorf("selfcheck: vote mutated, decide=%v", d)
	}
	// 不变量4：失败不留痕
	tx = New(2)
	if err := tx.Vote(0, true); err != nil {
		return fmt.Errorf("selfcheck: vote: %w", err)
	}
	y0, n0 := tx.Counts()
	if tx.Vote(-1, true) == nil || tx.Vote(2, true) == nil || tx.Vote(0, false) == nil {
		return errors.New("selfcheck: invalid op accepted")
	}
	if _, err := tx.Decide(); !errors.Is(err, ErrNotAllVoted) {
		return fmt.Errorf("selfcheck: early decide err=%v", err)
	}
	if y, nn := tx.Counts(); y != y0 || nn != n0 {
		return errors.New("selfcheck: rejected op changed state")
	}
	if err := tx.Vote(1, true); err != nil {
		return fmt.Errorf("selfcheck: unusable after rejection: %w", err)
	}
	if d, _ := tx.Decide(); d != coord.Commit {
		return fmt.Errorf("selfcheck: unanimous decide=%v", d)
	}
	return nil
}
