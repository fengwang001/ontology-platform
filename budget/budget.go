// Package budget 提供硬上限内存记账：超限必须失败，由调用方触发溢写。
package budget

import (
	"errors"
	"sync"
)

// ErrOverBudget 表示本次申请会突破硬上限。
var ErrOverBudget = errors.New("budget: over limit")

// Budget 是并发安全的驻留字节记账器。不变量：任意时刻 used <= limit。
type Budget struct {
	mu    sync.Mutex
	limit int64
	used  int64
	max   int64 // 历史峰值，非导出，经 Max() 读出
}

// New 构造上限为 limit 字节的记账器。
func New(limit int64) *Budget {
	return &Budget{limit: limit}
}

// Limit 返回硬上限。
func (b *Budget) Limit() int64 { return b.limit }

// TryAcquire 尝试记账 n 字节；若会超限则拒绝并返回 ErrOverBudget。
func (b *Budget) TryAcquire(n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > b.limit {
		return ErrOverBudget
	}
	b.used += n
	if b.used > b.max {
		b.max = b.used
	}
	return nil
}

// Release 释放 n 字节记账。
func (b *Budget) Release(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

// Used 返回当前驻留字节数。
func (b *Budget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Max 返回历史最大驻留字节数（永远 <= Limit）。
func (b *Budget) Max() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.max
}
