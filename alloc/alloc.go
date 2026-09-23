// Package alloc 在持久计数器租来的号段上做 O(1) 本地分发，
// 负责续租、段长自适应与时钟回拨检测。持久化永远先于分发。
package alloc

import (
	"errors"
	"sync"
	"time"

	"ontology/clock"
	"ontology/durable"
	"ontology/lease"
)

// ErrClockBackwards 表示检测到时钟回拨，分配被拒绝。
var ErrClockBackwards = errors.New("alloc: 时钟回拨，拒绝分配")

// Allocator 从共享持久计数器批量租段并在内存中分发，并发安全。
type Allocator struct {
	clk    clock.Clock
	ctr    *durable.Counter
	ttl    time.Duration
	minSeg int64
	maxSeg int64

	mu            sync.Mutex
	seg           int64 // 当前自适应段长
	cur           lease.Lease
	next          uint64 // 段内下一个待分发号
	hasCur        bool
	last          time.Time // 上次见到的时钟值，用于回拨检测
	persistWrites int       // 本实例触发的持久写次数（非导出计数器）
	ops           int       // 快速路径操作计数（验证 O(1)）
	history       []lease.Lease
}

// New 构造分配器。ttl 为租期（0 表示每次分配都重新租用）；
// minSeg/maxSeg 为段长自适应的上下界，要求 1 <= minSeg <= maxSeg。
func New(ctr *durable.Counter, clk clock.Clock, ttl time.Duration, minSeg, maxSeg int64) (*Allocator, error) {
	if minSeg < 1 || maxSeg < minSeg {
		return nil, lease.ErrInvalidLength
	}
	return &Allocator{clk: clk, ctr: ctr, ttl: ttl, minSeg: minSeg, maxSeg: maxSeg, seg: minSeg}, nil
}

// Alloc 分发下一个全局唯一的号。
func (a *Allocator) Alloc() (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clk.Now()
	if now.Before(a.last) {
		return 0, ErrClockBackwards
	}
	a.last = now
	if a.hasCur && a.next < a.cur.End() && !a.cur.Expired(now) {
		v := a.next // 快速路径：常数次操作，无持久写
		a.next++
		a.ops++
		return v, nil
	}
	if err := a.reacquire(now); err != nil {
		return 0, err
	}
	v := a.next
	a.next++
	return v, nil
}

// reacquire 作废旧租约（剩余号永不再发），先推进持久计数器再启用新段。
// 调用方须持锁。
func (a *Allocator) reacquire(now time.Time) error {
	if a.hasCur {
		if a.cur.Remaining(a.next) == 0 {
			a.seg = min(a.seg*2, a.maxSeg) // 段被耗尽：需求旺盛，倍增
		} else {
			a.seg = max(a.seg/2, a.minSeg) // 到期有剩余：空闲浪费，减半
		}
	}
	l, err := lease.New(0, a.seg, now.Add(a.ttl))
	if err != nil {
		return err
	}
	start, err := a.ctr.Next(l.Length) // 先落盘到 s+n，再允许分发
	if err != nil {
		return err
	}
	l.Start = start
	a.persistWrites++
	a.cur, a.next, a.hasCur = l, l.Start, true
	a.history = append(a.history, l)
	return nil
}

// PersistWrites 返回本实例触发的持久写次数。
func (a *Allocator) PersistWrites() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.persistWrites
}

// Ops 返回快速路径累计操作数（每次本地分发恰为 1，证明 O(1)）。
func (a *Allocator) Ops() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ops
}

// History 返回本实例租过的全部号段（按租用先后）。
func (a *Allocator) History() []lease.Lease {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]lease.Lease(nil), a.history...)
}
