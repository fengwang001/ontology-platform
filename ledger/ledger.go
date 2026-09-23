// Package ledger 维护加权信号量的额度账本：已占用、可用与不变量自检。
// 本包不依赖其他包；所有方法均非线程安全，由调用方（sem）在同一把锁内串行化。
package ledger

import "errors"

// ErrOverRelease 在释放导致占用为负时返回；此时账本保持不变。
var ErrOverRelease = errors.New("ledger: release exceeds acquired quota")

// Ledger 是总额度固定的账本。
type Ledger struct {
	cap  int64
	used int64
}

// New 创建总额度为 cap 的账本；非正容量会被规整为 0。
func New(cap int64) *Ledger {
	if cap < 0 {
		cap = 0
	}
	return &Ledger{cap: cap}
}

// Cap 返回总额度。
func (l *Ledger) Cap() int64 { return l.cap }

// Used 返回已占用额度。
func (l *Ledger) Used() int64 { return l.used }

// Available 返回当前可用额度。
func (l *Ledger) Available() int64 { return l.cap - l.used }

// CanReserve 报告权重 n 此刻能否立即满足。
func (l *Ledger) CanReserve(n int64) bool {
	return n >= 0 && n <= l.Available()
}

// Reserve 在不超发的前提下占用 n，返回是否成功。失败时账本不变。
func (l *Ledger) Reserve(n int64) bool {
	if !l.CanReserve(n) {
		return false
	}
	l.used += n
	return true
}

// Release 归还 n；若会使占用低于 0 则返回 ErrOverRelease 且账本不变。
func (l *Ledger) Release(n int64) error {
	if n < 0 || n > l.used {
		return ErrOverRelease
	}
	l.used -= n
	return nil
}

// Invariant 自检：占用必须落在 [0, Cap] 闭区间内。
func (l *Ledger) Invariant() bool {
	return l.used >= 0 && l.used <= l.cap
}
