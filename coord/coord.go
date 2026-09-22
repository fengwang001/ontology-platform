// Package coord 实现两阶段提交协调者。
//
// 协调者注入时钟（now func() time.Time），驱动两阶段：
// 第一阶段收集投票（Vote），裁决（Decide）全票同意才提交，
// 任一否决或超时（now >= deadline，左闭右开）则全体中止。
// 决议一旦写下不可更改；Drive 把决议投递给所有参与者，
// Replay 用于崩溃恢复时把已写下的决议重新投递一遍（幂等）。
//
// 空参与者集合的决议选择 Commit：空集合上「全票同意」是
// vacuously true，且提交一个没有任何参与者的事务无任何副作用，
// 比中止更安全、更符合直觉。该行为在本包内保持一致。
package coord

import (
	"errors"
	"sync"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// 参与者集合边界相关的可判定错误。
var (
	// ErrDuplicateParticipant 重复注册同一个参与者 ID。
	ErrDuplicateParticipant = errors.New("coord: duplicate participant id")
	// ErrUnknownParticipant 向未注册的参与者 ID 发指令。
	ErrUnknownParticipant = errors.New("coord: unknown participant id")
	// ErrVotingStarted 投票已开始后又试图注册新参与者。
	ErrVotingStarted = errors.New("coord: voting already started")
	// ErrNotDecided 尚未裁决就试图重放决议。
	ErrNotDecided = errors.New("coord: no decision to replay")
)

// Coordinator 驱动一次两阶段提交事务，并发安全。
type Coordinator struct {
	mu       sync.Mutex
	now      func() time.Time
	deadline time.Time
	order    []string
	parts    map[string]*participant.Participant
	tally    *vote.Tally
	outcome  vote.Outcome
	decided  bool
}

// New 创建协调者。now 为注入时钟，deadline 为第一阶段截止时刻
// （左闭右开：now 恰好等于 deadline 即算超时）。
func New(now func() time.Time, deadline time.Time) *Coordinator {
	return &Coordinator{
		now:      now,
		deadline: deadline,
		parts:    make(map[string]*participant.Participant),
	}
}

// Register 注册参与者。重复 ID 返回 ErrDuplicateParticipant；
// 投票开始后注册返回 ErrVotingStarted。
func (c *Coordinator) Register(p *participant.Participant) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tally != nil {
		return ErrVotingStarted
	}
	if _, ok := c.parts[p.ID()]; ok {
		return ErrDuplicateParticipant
	}
	c.parts[p.ID()] = p
	c.order = append(c.order, p.ID())
	return nil
}

// Vote 记录某参与者的一票。yes 表示同意（参与者进入已预备），
// 否则表示否决（参与者本地中止）。决议写下后迟到的票被忽略，
// 不会改写决议。未注册的 ID 返回 ErrUnknownParticipant。
func (c *Coordinator) Vote(id string, yes bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[id]
	if !ok {
		return ErrUnknownParticipant
	}
	if c.decided {
		return nil
	}
	var err error
	if yes {
		err = p.Prepare()
	} else {
		err = p.Abort()
	}
	if err != nil {
		return err
	}
	return c.tallyLocked().Cast(id, yes)
}

// Decide 只裁决不投递。未达终态时返回零值 vote.Outcome。
func (c *Coordinator) Decide() vote.Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decideLocked()
}

// Drive 裁决并把决议投递给所有参与者，返回决议。
// 重复调用幂等：决议保持第一次的结果，投递在参与者侧幂等。
func (c *Coordinator) Drive() vote.Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := c.decideLocked()
	if o.Decision != vote.Undecided {
		c.deliverLocked(o)
	}
	return o
}

// Replay 把已写下的决议重新投递给所有参与者，模拟崩溃恢复。
// 尚未裁决时返回 ErrNotDecided。重放幂等：参与者状态与本地
// 提交动作计数都与第一次投递后完全一致。
func (c *Coordinator) Replay() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.decided {
		return ErrNotDecided
	}
	c.deliverLocked(c.outcome)
	return nil
}

// decideLocked 裁决；决议只在第一次达终态时写下，之后不再改变。
func (c *Coordinator) decideLocked() vote.Outcome {
	if !c.decided {
		if o := c.tallyLocked().Decide(c.now()); o.Decision != vote.Undecided {
			c.outcome = o
			c.decided = true
		}
	}
	return c.outcome
}

// deliverLocked 把决议投递给所有参与者。参与者指令幂等，
// 此流程下 Apply 不会失败（提交时全员已预备，中止适用于任何状态）。
func (c *Coordinator) deliverLocked(o vote.Outcome) {
	for _, id := range c.order {
		_ = c.parts[id].Apply(o.Decision)
	}
}

// tallyLocked 惰性创建计票器。order 内无重复 ID，不会报错。
func (c *Coordinator) tallyLocked() *vote.Tally {
	if c.tally == nil {
		t, err := vote.NewTally(append([]string(nil), c.order...), c.deadline)
		if err != nil {
			panic(err)
		}
		c.tally = t
	}
	return c.tally
}
