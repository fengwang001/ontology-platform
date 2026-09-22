package coord

import (
	"ontology/participant"
	"ontology/vote"
)

// Phase 是事务当前所处阶段。
type Phase int

const (
	// PhaseVoting 第一阶段：投票中，尚未裁决。
	PhaseVoting Phase = iota
	// PhaseResolved 决议已写下并投递，事务进入终态。
	PhaseResolved
)

func (p Phase) String() string {
	if p == PhaseResolved {
		return "Resolved"
	}
	return "Voting"
}

// MemberStatus 是单个参与者的只读快照。
type MemberStatus struct {
	ID    string
	State participant.State
}

// Status 是事务的只读快照。未裁决时 Outcome 为零值（不猜测）。
type Status struct {
	Phase   Phase
	Decided bool
	Outcome vote.Outcome
	// Members 按注册顺序排列，保证多次查询结果稳定一致。
	Members []MemberStatus
}

// Query 返回事务当前阶段、决议与原因、每个参与者的当前状态。
// 已裁决后多次查询结果相同。
func (c *Coordinator) Query() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Status{Phase: PhaseVoting, Decided: c.decided}
	if c.decided {
		s.Phase = PhaseResolved
		s.Outcome = c.outcome
	}
	for _, id := range c.order {
		s.Members = append(s.Members, MemberStatus{
			ID:    id,
			State: c.parts[id].State(),
		})
	}
	return s
}
