package tcc

import (
	"errors"

	"ontology/ledger"
)

// 拒绝类别（优先级：参数非法 → ErrClock → 状态类 → 资源类）。
var (
	ErrInvalid      = errors.New("tcc: invalid argument")
	ErrClock        = errors.New("tcc: now before global clock or out of range")
	ErrHanging      = errors.New("tcc: hanging try blocked by earlier cancel")
	ErrMismatch     = errors.New("tcc: idempotent try with different account or amount")
	ErrState        = errors.New("tcc: branch already confirmed")
	ErrNoBranch     = errors.New("tcc: branch record not found")
	ErrExpired      = errors.New("tcc: reservation expired")
	ErrConflict     = errors.New("tcc: conflicting terminal state")
	ErrCapacity     = errors.New("tcc: branch record capacity full")
	ErrInsufficient = errors.New("tcc: insufficient available balance")
)

// ErrLimit 直接复用 ledger 的资源限额错误，errors.Is 可区分。
var ErrLimit = ledger.ErrLimit

// CancelReason 为取消原因。
type CancelReason string

const (
	// Cancel 显式 Cancel 导致。
	Cancel CancelReason = "Cancel"
	// Expired 预留到期自动取消。
	Expired CancelReason = "Expired"
	// Empty 空回滚：Cancel 先于 Try 到达。
	Empty CancelReason = "Empty"
)

// BranchState 为分支记录状态。
type BranchState int

const (
	StateTried BranchState = iota
	StateConfirmed
	StateCancelled
)

// Branch 是 (xid, br) 对应的分支记录。
type Branch struct {
	State    BranchState
	Acct     string
	Amount   int64
	Deadline int64 // 仅 Tried 有意义
	Reason   CancelReason
}
