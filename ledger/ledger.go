// Package ledger 维护多租户对象存储的用量与额度账本。
//
// 本包只提供线程安全的账本原语，不含任何校验策略；
// 拒绝次序与业务规则由上层 xfer 包实现。
package ledger

import "sync"

// Ledger 记录每个租户的当前用量与额度。
// 未设额度的租户额度视为 0。零值不可用，请使用 New。
type Ledger struct {
	mu    sync.Mutex
	usage map[string]int64
	limit map[string]int64
}

// New 返回一个空账本。
func New() *Ledger {
	return &Ledger{
		usage: make(map[string]int64),
		limit: make(map[string]int64),
	}
}

// SetLimit 设置租户额度，总被接受（可低于现有用量）。
func (l *Ledger) SetLimit(t string, q int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit[t] = q
}

// Limit 返回租户额度，未设置时为 0。
func (l *Ledger) Limit(t string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit[t]
}

// Usage 返回租户当前用量。
func (l *Ledger) Usage(t string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usage[t]
}

// Add 将租户用量增加 d（d 必须非负）。
func (l *Ledger) Add(t string, d int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.usage[t] += d
}

// Sub 将租户用量减少 d（d 必须非负）。
func (l *Ledger) Sub(t string, d int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.usage[t] -= d
}
