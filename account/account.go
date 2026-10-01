// Package account 实现基于托管区间的有界账户。
//
// 每个账户维护余额 b、下界 L、上界 H 与未决项上限 M。
// 事务对账户预留非零增量 d：负增量只检查区间下端，正增量只检查区间上端。
// 账户的可能区间为 [b+全部未决负增量之和, b+全部未决正增量之和]，
// 因此无论未决项的任意子集以任意顺序提交，余额都始终落在 [L, H] 内。
package account

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrTxCommitted 预留时事务已提交。
	ErrTxCommitted = errors.New("事务已提交")
	// ErrTxAborted 预留时事务已中止。
	ErrTxAborted = errors.New("事务已中止")
	// ErrAccountNotFound 账户不存在。
	ErrAccountNotFound = errors.New("账户不存在")
	// ErrZeroDelta 增量为零。
	ErrZeroDelta = errors.New("增量为零")
	// ErrTooManyPending 未决项数已达上限 M。
	ErrTooManyPending = errors.New("未决项数已达上限")
	// ErrBelowLower 预留后区间下端低于下界 L。
	ErrBelowLower = errors.New("下界不足")
	// ErrAboveUpper 预留后区间上端高于上界 H。
	ErrAboveUpper = errors.New("上界超出")
	// ErrTxFinished 提交或中止时事务已终结。
	ErrTxFinished = errors.New("事务已终结")
	// ErrTxNeverReserved 提交或中止时事务从未预留过。
	ErrTxNeverReserved = errors.New("事务从未预留")
	// ErrInvalidAccount 账户参数非法（L<=b<=H 不成立或 M<1）。
	ErrInvalidAccount = errors.New("账户参数非法")
	// ErrAccountExists 账户编号已存在。
	ErrAccountExists = errors.New("账户已存在")
)

// UncertainError 精确读在账户存在未决项时返回，附当前可能区间。
type UncertainError struct {
	Lo int64 // 区间下端：余额 + 全部未决负增量之和
	Hi int64 // 区间上端：余额 + 全部未决正增量之和
}

func (e *UncertainError) Error() string {
	return fmt.Sprintf("余额不确定: 可能区间 [%d, %d]", e.Lo, e.Hi)
}

// account 是单个有界账户的内部状态。
type account struct {
	balance    int64 // 当前余额（只含已提交增量）
	lo, hi     int64 // 下界 L 与上界 H
	maxPending int   // 未决项上限 M
	negSum     int64 // 全部未决负增量之和（<=0）
	posSum     int64 // 全部未决正增量之和（>=0）
	pending    int   // 当前未决项数
}

// txStatus 是事务生命周期状态。
type txStatus int

const (
	txOpen txStatus = iota
	txCommitted
	txAborted
)

// entry 是事务在某个账户上的一条已接受预留。
type entry struct {
	accountID string
	delta     int64
}

// tx 记录一个事务的全部未决预留与状态。
type tx struct {
	status  txStatus
	entries []entry
}

// Ledger 管理一组有界账户与事务，全部方法可并发调用。
type Ledger struct {
	mu       sync.Mutex
	accounts map[string]*account
	txs      map[string]*tx
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{
		accounts: make(map[string]*account),
		txs:      make(map[string]*tx),
	}
}

// AddAccount 创建账户。要求 L <= b <= H 且 M >= 1，否则整体拒绝。
func (l *Ledger) AddAccount(id string, b, lo, hi int64, maxPending int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lo > b || b > hi || maxPending < 1 {
		return ErrInvalidAccount
	}
	if _, ok := l.accounts[id]; ok {
		return ErrAccountExists
	}
	l.accounts[id] = &account{balance: b, lo: lo, hi: hi, maxPending: maxPending}
	return nil
}

// Reserve 让事务 txID 在账户 accountID 上预留增量 d。
// 非法情形按固定顺序只报第一个：事务已提交或已中止、账户不存在、
// 增量为零、未决项数已达 M、下界不足或上界超出。
// 被拒绝时不改变任何状态；接受时不检查另一侧边界。
func (l *Ledger) Reserve(txID, accountID string, d int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if t, ok := l.txs[txID]; ok {
		switch t.status {
		case txCommitted:
			return ErrTxCommitted
		case txAborted:
			return ErrTxAborted
		}
	}
	acc, ok := l.accounts[accountID]
	if !ok {
		return ErrAccountNotFound
	}
	if d == 0 {
		return ErrZeroDelta
	}
	if acc.pending >= acc.maxPending {
		return ErrTooManyPending
	}
	if d < 0 {
		if acc.balance+acc.negSum+d < acc.lo {
			return ErrBelowLower
		}
		acc.negSum += d
	} else {
		if acc.balance+acc.posSum+d > acc.hi {
			return ErrAboveUpper
		}
		acc.posSum += d
	}
	acc.pending++

	t, ok := l.txs[txID]
	if !ok {
		t = &tx{}
		l.txs[txID] = t
	}
	t.entries = append(t.entries, entry{accountID: accountID, delta: d})
	return nil
}

// Commit 把事务在所有账户上的未决项并入余额，必然成功；
// 事务已终结或从未预留过则整体拒绝。
func (l *Ledger) Commit(txID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	t, ok := l.txs[txID]
	if !ok {
		return ErrTxNeverReserved
	}
	if t.status != txOpen {
		return ErrTxFinished
	}
	for _, e := range t.entries {
		acc := l.accounts[e.accountID]
		acc.balance += e.delta
		if e.delta < 0 {
			acc.negSum -= e.delta
		} else {
			acc.posSum -= e.delta
		}
		acc.pending--
	}
	t.status = txCommitted
	t.entries = nil
	return nil
}

// Abort 丢弃事务在所有账户上的未决项；
// 事务已终结或从未预留过则整体拒绝。
func (l *Ledger) Abort(txID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	t, ok := l.txs[txID]
	if !ok {
		return ErrTxNeverReserved
	}
	if t.status != txOpen {
		return ErrTxFinished
	}
	for _, e := range t.entries {
		acc := l.accounts[e.accountID]
		if e.delta < 0 {
			acc.negSum -= e.delta
		} else {
			acc.posSum -= e.delta
		}
		acc.pending--
	}
	t.status = txAborted
	t.entries = nil
	return nil
}

// Read 精确读：账户无任何未决项时返回余额，
// 否则返回 *UncertainError 并附当前可能区间。
func (l *Ledger) Read(accountID string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	acc, ok := l.accounts[accountID]
	if !ok {
		return 0, ErrAccountNotFound
	}
	if acc.pending == 0 {
		return acc.balance, nil
	}
	return 0, &UncertainError{Lo: acc.balance + acc.negSum, Hi: acc.balance + acc.posSum}
}
