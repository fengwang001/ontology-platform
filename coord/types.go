package coord

import (
	"errors"

	"ontology/participant"
	"ontology/vote"
)

// 协调者可判定的错误。
var (
	// ErrDuplicateParticipant 表示重复注册同一参与者 ID。
	ErrDuplicateParticipant = errors.New("coord: duplicate participant id")
	// ErrUnknownParticipant 表示向未注册的 ID 投票或发指令。
	ErrUnknownParticipant = errors.New("coord: unknown participant id")
	// ErrNotStarted 表示事务尚未 Begin。
	ErrNotStarted = errors.New("coord: transaction not started")
	// ErrAlreadyStarted 表示重复 Begin。
	ErrAlreadyStarted = errors.New("coord: transaction already started")
	// ErrAlreadyDecided 表示决议写下后试图改变事务结构。
	ErrAlreadyDecided = errors.New("coord: transaction already decided")
	// ErrNoDecision 表示尚未裁决就请求重放。
	ErrNoDecision = errors.New("coord: no decision to replay")
)

// Phase 是事务当前阶段。
type Phase int

const (
	// PhaseNotStarted 表示尚未 Begin。
	PhaseNotStarted Phase = iota
	// PhaseVoting 表示第一阶段投票中，尚未裁决。
	PhaseVoting
	// PhaseDecided 表示决议已写下（提交或中止）。
	PhaseDecided
)

// Status 是事务的只读快照。
type Status struct {
	Phase        Phase
	Verdict      vote.Verdict // 未裁决时为零值
	Participants map[string]participant.State
	// CommitCounts 是每个参与者本地提交动作的执行次数，供核对幂等性。
	CommitCounts map[string]int
}
