// Package ledger 维护加权额度账本：已占用、可用与自检。
package ledger

import "errors"

// ErrOverRelease 表示释放后占用会变为负数。
var ErrOverRelease = errors.New("ledger: release exceeds acquired amount")

// Ledger 是受外层互斥保护的纯账本；自身不加锁，便于与队列共用一把锁。
type Ledger struct {
	cap  int64
	used int64
}

// New 创建总额度为 cap 的账本；非正容量取 panic 表示编程错误。
func New(cap int64) *Ledger {
	if cap <= 0 {
		panic("ledger: capacity must be positive")
	}
	return &Ledger{cap: cap}
}

// Cap 返回总额度。
func (l *Ledger) Cap() int64 { return l.cap }

// Used 返回已占用额度。
func (l *Ledger) Used() int64 { return l.used }

// Available 返回当前可用额度。
func (l *Ledger) Available() int64 { return l.cap - l.used }

// CanAllocate 报告权重 n 此刻能否被满足。
func (l *Ledger) CanAllocate(n int64) bool { return n > 0 && n <= l.Available() }

// Allocate 预占 n 额度；调用方须先用 CanAllocate 判定。
func (l *Ledger) Allocate(n int64) { l.used += n }

// Release 归还 n 额度；会导致占用为负时拒绝且账本不变。
func (l *Ledger) Release(n int64) error {
	if n > l.used {
		return ErrOverRelease
	}
	l.used -= n
	return nil
}

// Healthy 是自检不变量：占用落在 [0, Cap]。
func (l *Ledger) Healthy() bool { return 0 <= l.used && l.used <= l.cap }
