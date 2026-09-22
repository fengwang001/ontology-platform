// Package participant 实现两阶段提交中参与者的本地状态机。
//
// 状态机：Pending -> Prepared -> Committed
//
//	Pending/Prepared -> Aborted
//
// 指令幂等：已提交者重复收到提交、已中止者重复收到中止，状态不变、
// 本地提交动作计数不增加。非法转移返回彼此可区分的错误且不改状态。
package participant

import (
	"errors"
	"sync"

	"ontology/vote"
)

// State 是参与者的本地状态。
type State int

const (
	// Pending 待定：尚未预备。
	Pending State = iota
	// Prepared 已预备：第一阶段已同意，等待决议。
	Prepared
	// Committed 已提交：终态。
	Committed
	// Aborted 已中止：终态。
	Aborted
)

func (s State) String() string {
	switch s {
	case Prepared:
		return "Prepared"
	case Committed:
		return "Committed"
	case Aborted:
		return "Aborted"
	default:
		return "Pending"
	}
}

// 三种非法转移的错误，彼此可区分（可用 errors.Is 判定）。
var (
	// ErrCommitBeforePrepare 未预备就收到提交指令。
	ErrCommitBeforePrepare = errors.New("participant: commit before prepare")
	// ErrCommitAfterAbort 已中止后又收到提交指令。
	ErrCommitAfterAbort = errors.New("participant: commit after abort")
	// ErrAbortAfterCommit 已提交后又收到中止指令。
	ErrAbortAfterCommit = errors.New("participant: abort after commit")
	// ErrPrepareInFinalState 已提交/已中止后又收到预备请求。
	ErrPrepareInFinalState = errors.New("participant: prepare in final state")
	// ErrApplyUndecided 试图按「未裁决」驱动状态机。
	ErrApplyUndecided = errors.New("participant: cannot apply undecided decision")
)

// Participant 是一个参与者的本地状态机，并发安全。
type Participant struct {
	mu          sync.Mutex
	id          string
	state       State
	commitCount int
}

// New 创建一个处于 Pending 态的参与者。
func New(id string) *Participant {
	return &Participant{id: id}
}

// ID 返回参与者 ID。
func (p *Participant) ID() string {
	return p.id
}

// State 返回当前状态。
func (p *Participant) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// CommitCount 返回本地提交动作实际执行的次数（用于核对幂等性）。
func (p *Participant) CommitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commitCount
}

// Prepare 处理第一阶段的预备请求：Pending -> Prepared。
// 已预备时幂等；终态下报错。
func (p *Participant) Prepare() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case Pending:
		p.state = Prepared
		return nil
	case Prepared:
		return nil
	default:
		return ErrPrepareInFinalState
	}
}

// Commit 处理提交指令：Prepared -> Committed，副作用计数加一。
// 已提交时幂等（状态不变、计数不增）；其余状态报可区分的错误。
func (p *Participant) Commit() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case Prepared:
		p.state = Committed
		p.commitCount++
		return nil
	case Committed:
		return nil
	case Pending:
		return ErrCommitBeforePrepare
	default:
		return ErrCommitAfterAbort
	}
}

// Abort 处理中止指令：Pending/Prepared -> Aborted。
// 已中止时幂等；已提交时报可区分的错误。
func (p *Participant) Abort() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case Pending, Prepared:
		p.state = Aborted
		return nil
	case Aborted:
		return nil
	default:
		return ErrAbortAfterCommit
	}
}

// Apply 按协调者写下的决议驱动本地状态机；重复投递幂等。
func (p *Participant) Apply(d vote.Decision) error {
	switch d {
	case vote.Commit:
		return p.Commit()
	case vote.Abort:
		return p.Abort()
	default:
		return ErrApplyUndecided
	}
}
