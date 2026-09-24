// Package ledger 维护加权信号量的额度账本，不依赖其他包。
package ledger

import "errors"

// ErrOverRelease 在释放导致占用为负时返回，且账本保持不变。
var ErrOverRelease = errors.New("ledger: release exceeds acquired amount")

// Ledger 记录总额度与已占用额度。零值不可用，必须用 New 构造。
type Ledger struct {
	cap  int64
	used int64
}

// New 创建总额度为 cap 的账本。
func New(cap int64) *Ledger { return &Ledger{cap: cap} }

// Cap 返回总额度。
func (l *Ledger) Cap() int64 { return l.cap }

// Used 返回已占用额度。
func (l *Ledger) Used() int64 { return l.used }

// Available 返回当前可用额度。
func (l *Ledger) Available() int64 { return l.cap - l.used }

// CanAllocate 报告权重 n 是否不超过可用额度。
func (l *Ledger) CanAllocate(n int64) bool { return n <= l.Available() }

// Allocate 在额度足够时占用 n 并返回 true，否则账本不变并返回 false。
func (l *Ledger) Allocate(n int64) bool {
	if !l.CanAllocate(n) {
		return false
	}
	l.used += n
	return true
}

// Release 归还 n；若会使占用为负则返回 ErrOverRelease 且账本不变。
func (l *Ledger) Release(n int64) error {
	if n > l.used {
		return ErrOverRelease
	}
	l.used -= n
	return nil
}

// Check 自检占用恒在 [0, Cap] 内。
func (l *Ledger) Check() bool { return l.used >= 0 && l.used <= l.cap }
