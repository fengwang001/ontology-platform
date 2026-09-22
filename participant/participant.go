// Package participant 实现两阶段提交中参与者的本地状态机。
//
// 状态机：Pending -> Prepared -> Committed，或 Pending/Prepared -> Aborted。
// 提交与中止指令都是幂等的：重复投递同一指令状态不变、副作用只发生一次。
// 非法转移返回彼此可区分的错误，且不改变已有状态。
package participant

import (
	"errors"
	"sync"

	"ontology/vote"
)

// State 是参与者的本地状态。
type State int

const (
	// Pending 表示已注册但尚未预备。
	Pending State = iota
	// Prepared 表示已完成第一阶段预备，等待决议。
	Prepared
	// Committed 表示已提交（终态）。
	Committed
	// Aborted 表示已中止（终态）。
	Aborted
)

// String 便于日志与演示程序打印。
func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Prepared:
		return "Prepared"
	case Committed:
		return "Committed"
	case Aborted:
		return "Aborted"
	default:
		return "Unknown"
	}
}

// 三种非法转移的错误，彼此可区分，可用 errors.Is 判定。
var (
	// ErrNotPrepared 表示未预备就收到提交指令。
	ErrNotPrepared = errors.New("participant: commit before prepare")
	// ErrCommitAfterAbort 表示已中止后又收到提交指令。
	ErrCommitAfterAbort = errors.New("participant: commit after abort")
	// ErrAbortAfterCommit 表示已提交后又收到中止指令。
	ErrAbortAfterCommit = errors.New("participant: abort after commit")
	// ErrPrepareAfterFinal 表示进入终态后又收到预备请求。
	ErrPrepareAfterFinal = errors.New("participant: prepare after final state")
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
	return &Participant{id: id, state: Pending}
}

// ID 返回参与者 ID。
func (p *Participant) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id
}

// State 返回当前状态。
func (p *Participant) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// CommitCount 返回本地提交动作的实际执行次数，供测试核对幂等性。
func (p *Participant) CommitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commitCount
}

// Prepare 执行第一阶段预备：Pending -> Prepared。
// 重复预备是幂等的；进入终态后预备返回 ErrPrepareAfterFinal。
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
		return ErrPrepareAfterFinal
	}
}

// Commit 执行提交指令：Prepared -> Committed，副作用计数加一。
// 重复提交幂等（状态与计数均不变）；非法转移返回可区分错误且状态不变。
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
	case Aborted:
		return ErrCommitAfterAbort
	default:
		return ErrNotPrepared
	}
}

// Abort 执行中止指令：Pending/Prepared -> Aborted。
// 重复中止幂等；已提交后中止返回 ErrAbortAfterCommit 且状态不变。
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

// Deliver 按决议投递指令：Commit 走 Commit，Abort 走 Abort。
// 其他决议值（含 None）不是合法指令，返回错误且不改变状态。
func (p *Participant) Deliver(d vote.Decision) error {
	switch d {
	case vote.Commit:
		return p.Commit()
	case vote.Abort:
		return p.Abort()
	default:
		return errors.New("participant: cannot deliver undecided verdict")
	}
}
