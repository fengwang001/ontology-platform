// Package coord 实现两阶段提交协调者。
//
// 协调者注入时钟（now func() time.Time），驱动两阶段：
// 第一阶段收集投票，全票同意才提交，任一否决或超时全体中止。
// 决议一旦写下不可更改；对参与者的提交/中止指令幂等。
//
// 空参与者集合的决议为 Commit：空集上「全票同意」vacuously 成立，
// 且无可中止的参与者，提交是唯一的确定结果。
package coord

import (
	"sync"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// Coordinator 驱动一次两阶段提交，并发安全。
type Coordinator struct {
	now func() time.Time

	mu           sync.Mutex
	order        []string
	participants map[string]*participant.Participant
	votes        map[string]vote.Outcome
	begun        bool
	deadline     time.Time
	decided      bool
	verdict      vote.Verdict
}

// New 创建协调者，注入时钟 now；实现内不得直接使用 time.Now 等。
func New(now func() time.Time) *Coordinator {
	return &Coordinator{
		now:          now,
		participants: make(map[string]*participant.Participant),
		votes:        make(map[string]vote.Outcome),
	}
}

// Register 注册参与者；重复 ID 返回 ErrDuplicateParticipant。
func (c *Coordinator) Register(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decided {
		return ErrAlreadyDecided
	}
	if _, ok := c.participants[id]; ok {
		return ErrDuplicateParticipant
	}
	c.participants[id] = participant.New(id)
	c.order = append(c.order, id)
	return nil
}

// Begin 开启事务并设置第一阶段超时（相对注入时钟）。
func (c *Coordinator) Begin(timeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.begun {
		return ErrAlreadyStarted
	}
	c.begun = true
	c.deadline = c.now().Add(timeout)
	return nil
}

// CastVote 记录参与者的投票。同意票意味着该参与者完成预备。
// 决议写下后迟到的票被接受但忽略，决议与原因保持第一次的结果。
func (c *Coordinator) CastVote(id string, outcome vote.Outcome) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.participants[id]
	if !ok {
		return ErrUnknownParticipant
	}
	if !c.begun {
		return ErrNotStarted
	}
	if c.decided {
		return nil
	}
	if outcome == vote.Agree {
		if err := p.Prepare(); err != nil {
			return err
		}
	}
	c.votes[id] = outcome
	return nil
}

// Drive 驱动裁决：全票同意则提交，任一否决或超时则全体中止。
// 决议只写一次；重复调用返回第一次的结果。尚未集齐且未超时时
// 返回零值 Verdict 表示继续等待。
func (c *Coordinator) Drive() (vote.Verdict, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.begun {
		return vote.Verdict{}, ErrNotStarted
	}
	if c.decided {
		return c.verdict, nil
	}
	expired := !c.now().Before(c.deadline) // 左闭右开：now == deadline 即超时
	v := vote.Decide(c.order, c.votes, expired)
	if v.Decision == vote.None {
		return vote.Verdict{}, nil
	}
	c.verdict = v
	c.decided = true
	return c.verdict, c.deliverLocked()
}

// SendCommand 向指定参与者投递决议指令；未注册 ID 返回错误。
func (c *Coordinator) SendCommand(id string, d vote.Decision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.participants[id]
	if !ok {
		return ErrUnknownParticipant
	}
	return p.Deliver(d)
}

// Replay 把已写下的决议重新投递给所有参与者，用于崩溃恢复。
// 指令幂等，重放后参与者状态与本地提交计数均不变化。
func (c *Coordinator) Replay() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.decided {
		return ErrNoDecision
	}
	return c.deliverLocked()
}

// Query 返回事务只读快照；未裁决时 Verdict 为零值，已裁决后结果稳定。
func (c *Coordinator) Query() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Status{
		Participants: make(map[string]participant.State, len(c.order)),
		CommitCounts: make(map[string]int, len(c.order)),
	}
	switch {
	case c.decided:
		s.Phase = PhaseDecided
		s.Verdict = c.verdict
	case c.begun:
		s.Phase = PhaseVoting
	default:
		s.Phase = PhaseNotStarted
	}
	for _, id := range c.order {
		s.Participants[id] = c.participants[id].State()
		s.CommitCounts[id] = c.participants[id].CommitCount()
	}
	return s
}

// deliverLocked 把已写下的决议投递给所有参与者，返回首个投递错误。
func (c *Coordinator) deliverLocked() error {
	var first error
	for _, id := range c.order {
		if err := c.participants[id].Deliver(c.verdict.Decision); err != nil && first == nil {
			first = err
		}
	}
	return first
}
