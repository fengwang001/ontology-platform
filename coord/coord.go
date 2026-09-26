// Package coord 实现 2PC 协调者：聚合各参与者投票并做出 Commit/Abort 决定。
package coord

import (
	"errors"
	"sync"

	"ontology/pt"
)

// 可判定的哨兵错误。
var (
	// ErrOutOfRange 参与者下标越界。
	ErrOutOfRange = errors.New("coord: participant index out of range")
	// ErrNotAllVoted 尚有参与者未投票就调用 Decide。
	ErrNotAllVoted = errors.New("coord: not all participants have voted")
)

// Decision 是协调者的决定。
type Decision int

const (
	// Pending 未决。
	Pending Decision = iota
	// Commit 全体一致提交。
	Commit
	// Abort 中止。
	Abort
)

func (d Decision) String() string {
	switch d {
	case Commit:
		return "Commit"
	case Abort:
		return "Abort"
	default:
		return "Pending"
	}
}

// Coordinator 协调 n 个参与者就一个事务投票。
type Coordinator struct {
	mu       sync.Mutex
	ballots  []pt.Ballot
	yes, no  int
	decided  bool
	decision Decision
	checked  int // 最近一次 Decide/Recover 检查过的参与者个数（增量计数，不扫表）
}

// New 创建有 n 个参与者的协调者。
func New(n int) *Coordinator {
	return &Coordinator{ballots: make([]pt.Ballot, n)}
}

// Vote 参与者 p 投票。越界返回 ErrOutOfRange，重复投票返回 pt.ErrDuplicateVote；
// 失败时不改变任何状态。
func (c *Coordinator) Vote(p int, yes bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p < 0 || p >= len(c.ballots) {
		return ErrOutOfRange
	}
	if err := c.ballots[p].Cast(yes); err != nil {
		return err
	}
	if yes {
		c.yes++
	} else {
		c.no++
	}
	return nil
}

// Decide 在所有参与者都投过票后给出决定：全 yes → Commit，否则 Abort。
// 未投齐返回 ErrNotAllVoted 且状态不变。已决定则保持原决定。
func (c *Coordinator) Decide() (Decision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0 // 直接用增量计数判定，不扫描任何参与者
	if c.decided {
		return c.decision, nil
	}
	if c.yes+c.no < len(c.ballots) {
		return Pending, ErrNotAllVoted
	}
	c.decideLocked()
	return c.decision, nil
}

// Recover 崩溃恢复：已决定则保持；已投齐则按规则决定；否则安全中止。
func (c *Coordinator) Recover() Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0
	if !c.decided {
		if c.yes+c.no == len(c.ballots) {
			c.decideLocked()
		} else {
			c.decision, c.decided = Abort, true
		}
	}
	return c.decision
}

// decideLocked 在已投齐的前提下按全体一致规则落定决定。调用方须持锁。
func (c *Coordinator) decideLocked() {
	if c.no == 0 && c.yes == len(c.ballots) {
		c.decision = Commit
	} else {
		c.decision = Abort
	}
	c.decided = true
}

// Counts 返回当前 yes/no 票数。
func (c *Coordinator) Counts() (yes, no int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.yes, c.no
}
