// Package ledger 管理账户余额与冻结额，是 TCC 资源预留的账目基础。
package ledger

import "errors"

// 共享错误：状态类与资源类，errors.Is 可直接区分。
var (
	ErrParam        = errors.New("ledger: invalid parameter")
	ErrHanging      = errors.New("tcc: hanging attempt blocked by prior cancel")
	ErrMismatch     = errors.New("tcc: branch retried with different account or amount")
	ErrState        = errors.New("tcc: branch already confirmed")
	ErrNoBranch     = errors.New("tcc: branch record not found")
	ErrExpired      = errors.New("tcc: reservation expired")
	ErrConflict     = errors.New("tcc: operation conflicts with branch state")
	ErrCapacity     = errors.New("tcc: branch capacity exhausted")
	ErrInsufficient = errors.New("ledger: insufficient available balance")
	ErrLimit        = errors.New("ledger: account balance limit exceeded")
	ErrClock        = errors.New("tcc: non-monotonic or invalid clock")
	ErrExists       = errors.New("coord: transaction already exists")
	ErrNoTx         = errors.New("coord: transaction not found")
	ErrDecision     = errors.New("coord: decision already committed")
)

const (
	MaxBalance = 1_000_000_000_000_000 // 1e15
	MaxDeposit = 1_000_000_000_000     // 单次充值上限 1e12
)

// Ledger 保存所有账户的余额与冻结额。零值即可用。
type Ledger struct {
	bal map[string]int64
	fz  map[string]int64
}

// New 创建空账本。
func New() *Ledger {
	return &Ledger{bal: map[string]int64{}, fz: map[string]int64{}}
}

func validAcct(acct []byte) bool { return len(acct) > 0 }

// Balance 返回账户余额（从未出现的账户为 0）。
func (l *Ledger) Balance(acct []byte) int64 { return l.bal[string(acct)] }

// Frozen 返回账户冻结额。
func (l *Ledger) Frozen(acct []byte) int64 { return l.fz[string(acct)] }

// Avail 返回可用额 bal-fz。
func (l *Ledger) Avail(acct []byte) int64 {
	k := string(acct)
	return l.bal[k] - l.fz[k]
}

// Deposit 给账户充值 x ∈ [1,1e12]，充值后余额不得超过 1e15，否则 ErrLimit 且不改账户。
func (l *Ledger) Deposit(acct []byte, x int64) error {
	if !validAcct(acct) || x < 1 || x > MaxDeposit {
		return ErrParam
	}
	k := string(acct)
	if l.bal[k]+x > MaxBalance {
		return ErrLimit
	}
	l.bal[k] += x
	return nil
}

// Freeze 冻结 amount：可用额不足返回 ErrInsufficient（相等通过）。
func (l *Ledger) Freeze(acct []byte, amount int64) error {
	k := string(acct)
	if l.bal[k]-l.fz[k] < amount {
		return ErrInsufficient
	}
	l.fz[k] += amount
	return nil
}

// Unfreeze 释放冻结额（取消/到期路径）。
func (l *Ledger) Unfreeze(acct []byte, amount int64) {
	k := string(acct)
	l.fz[k] -= amount
}

// Confirm 确认扣减：bal 与 fz 同时减 amount。
func (l *Ledger) Confirm(acct []byte, amount int64) {
	k := string(acct)
	l.fz[k] -= amount
	l.bal[k] -= amount
}

// FrozenTotal 返回所有账户冻结额之和（测试不变量用）。
func (l *Ledger) FrozenTotal() int64 {
	var sum int64
	for _, v := range l.fz {
		sum += v
	}
	return sum
}
