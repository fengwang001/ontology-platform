// Package txid 提供事务号的分配与比较。
//
// 事务号单调递增、不回绕、零值非法。
// 分配只通过注入的 Source 进行，包内不读取任何真实时钟。
package txid

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// ID 是事务号。零值（Invalid）非法，合法值从 1 开始单调递增。
type ID uint64

// Invalid 是非法事务号（零值）。
const Invalid ID = 0

// ErrExhausted 表示事务号空间耗尽（拒绝回绕）。
var ErrExhausted = errors.New("txid: 事务号空间耗尽，拒绝回绕")

// Valid 报告事务号是否合法（非零）。
func (a ID) Valid() bool { return a != Invalid }

// Before 报告 a 是否严格小于 b。
func (a ID) Before(b ID) bool { return a < b }

// After 报告 a 是否严格大于 b。
func (a ID) After(b ID) bool { return a > b }

func (a ID) String() string { return fmt.Sprintf("tx#%d", uint64(a)) }

// Source 是注入的事务号源。实现必须保证单调递增且不回绕。
type Source interface {
	// Next 分配下一个事务号。
	Next() (ID, error)
	// Current 返回最近一次已分配的事务号；从未分配过时返回 Invalid。
	Current() ID
}

// Counter 是进程内的单调计数事务号源，并发安全。
type Counter struct {
	mu   sync.Mutex
	last ID
}

// NewCounter 返回一个从 1 开始分配的计数源。
func NewCounter() *Counter { return &Counter{} }

// Next 分配下一个事务号。到达 math.MaxUint64 后返回 ErrExhausted 而不是回绕。
func (c *Counter) Next() (ID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == ID(math.MaxUint64) {
		return Invalid, ErrExhausted
	}
	c.last++
	return c.last, nil
}

// Current 返回最近一次分配的事务号。
func (c *Counter) Current() ID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}
