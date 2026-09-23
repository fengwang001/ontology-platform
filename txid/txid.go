// Package txid 提供事务号的分配与比较。
// 事务号单调递增、不回绕、零值非法；只走注入源，不依赖其他包。
package txid

import (
	"errors"
	"sync/atomic"
)

// T 是事务号。零值 Invalid 非法，表示「没有事务」。
type T uint64

// Invalid 是非法事务号（零值）。
const Invalid T = 0

// ErrExhausted 表示事务号空间耗尽（拒绝回绕）。
var ErrExhausted = errors.New("txid: id space exhausted")

// Source 是事务号注入源。store 只依赖该接口，测试可注入假源。
type Source interface {
	// Next 分配下一个事务号，单调递增；耗尽时返回 ErrExhausted。
	Next() (T, error)
	// Peek 返回「下一个将被分配的号」，不消耗。所有已分配的号都 < Peek()。
	Peek() T
}

// Counter 是进程内存实现的事务号源，并发安全。
type Counter struct {
	cur atomic.Uint64
}

// NewCounter 返回从 1 开始分配的事务号源（零值非法）。
func NewCounter() *Counter {
	return &Counter{}
}

// Next 实现 Source。
func (c *Counter) Next() (T, error) {
	for {
		cur := c.cur.Load()
		if cur == ^uint64(0) {
			return Invalid, ErrExhausted
		}
		if c.cur.CompareAndSwap(cur, cur+1) {
			return T(cur + 1), nil
		}
	}
}

// Peek 实现 Source。
func (c *Counter) Peek() T {
	return T(c.cur.Load() + 1)
}
