// Package ledger 维护账户余额 bal 与冻结额 fz。
// 可用额 = bal - fz。所有方法对并发安全。
package ledger

import (
	"errors"
	"sync"
)

const maxBalance = 1_000_000_000_000_000 // 1e15

var (
	// ErrLimit 账户余额超过 1e15；此时不修改任何账户。
	ErrLimit = errors.New("ledger: balance limit exceeded")
	// ErrInvalid 参数非法（空账户名或金额越界）。
	ErrInvalid = errors.New("ledger: invalid argument")
)

type account struct {
	bal int64
	fz  int64
}

// Ledger 是账户集合。零值不可用，须用 New 构造。
type Ledger struct {
	mu sync.Mutex
	m  map[string]*account
}

// New 创建空账本。
func New() *Ledger {
	return &Ledger{m: make(map[string]*account)}
}

// Deposit 向 acct 存入 x（1..1e12）。入账后余额不得超过 1e15，
// 否则返回 ErrLimit 且账户不变。
func (l *Ledger) Deposit(acct string, x int64) error {
	if acct == "" || x < 1 || x > 1_000_000_000_000 {
		return ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	if a.bal+x > maxBalance {
		return ErrLimit
	}
	a.bal += x
	return nil
}

// Bal 返回账户余额（从未出现的账户为 0）。
func (l *Ledger) Bal(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(acct).bal
}

// Fz 返回账户冻结额。
func (l *Ledger) Fz(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(acct).fz
}

// Avail 返回可用额 bal-fz。
func (l *Ledger) Avail(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	return a.bal - a.fz
}

// Freeze 在可用额不少于 x 时增加冻结额，否则不改动并返回 false。
func (l *Ledger) Freeze(acct string, x int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	if a.bal-a.fz < x {
		return false
	}
	a.fz += x
	return true
}

// Unfreeze 释放冻结额（取消/到期路径）。
func (l *Ledger) Unfreeze(acct string, x int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	a.fz -= x
}

// Confirm 把冻结额转为扣款：bal 与 fz 同时减少 x。
func (l *Ledger) Confirm(acct string, x int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	a.fz -= x
	a.bal -= x
}

// get 调用方须持锁。
func (l *Ledger) get(acct string) *account {
	a := l.m[acct]
	if a == nil {
		a = &account{}
		l.m[acct] = a
	}
	return a
}
