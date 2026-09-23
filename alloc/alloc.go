// Package alloc 在租用的号段内做本地 O(1) 分发，并负责续租与段长自适应。
package alloc

import (
	"errors"
	"sync"
	"time"

	"ontology/clock"
	"ontology/durable"
	"ontology/lease"
)

// ErrClockRollback 在检测到时钟回拨时返回，分配被拒绝。
var ErrClockRollback = errors.New("alloc: clock rollback detected")

// Config 配置租期与段长自适应边界。TTL 为 0 表示每次分配都重新租用。
type Config struct {
	TTL    time.Duration
	MinSeg int64 // 段长下保底，默认 64
	MaxSeg int64 // 段长封顶，默认 4096
}

func (c Config) withDefaults() Config {
	if c.MinSeg <= 0 {
		c.MinSeg = 64
	}
	if c.MaxSeg <= 0 {
		c.MaxSeg = 4096
	}
	if c.MaxSeg < c.MinSeg {
		c.MaxSeg = c.MinSeg
	}
	return c
}

// Allocator 从共享持久计数器租段，在本地分发号。
type Allocator struct {
	clk clock.Clock
	ctr *durable.Counter
	cfg Config

	mu       sync.Mutex
	seg      lease.Lease
	next     uint64
	segLen   int64
	lastNow  time.Time
	seen     bool
	persists uint64 // 持久写次数（非导出计数器）
	ops      uint64 // 本地分发操作数（O(1) 断言用）
}

// New 创建一个分配器。
func New(clk clock.Clock, ctr *durable.Counter, cfg Config) *Allocator {
	cfg = cfg.withDefaults()
	return &Allocator{clk: clk, ctr: ctr, cfg: cfg, segLen: cfg.MinSeg}
}

// Next 分发下一个号；时钟回拨或持久写失败时返回错误。
func (a *Allocator) Next() (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clk.Now()
	if a.seen && now.Before(a.lastNow) {
		return 0, ErrClockRollback
	}
	a.lastNow, a.seen = now, true
	if a.next >= a.seg.End() || !a.seg.ValidAt(now) {
		if err := a.acquire(now); err != nil {
			return 0, err
		}
	}
	id := a.next
	a.next++
	a.ops++
	return id, nil
}

// acquire 租用新号段：原子推进持久计数器，落盘成功后才可分发。
func (a *Allocator) acquire(now time.Time) error {
	a.adapt(now)
	start, err := a.ctr.AdvanceBy(uint64(a.segLen))
	if err != nil {
		return err
	}
	seg, err := lease.New(start, a.segLen, now.Add(a.cfg.TTL))
	if err != nil {
		return err
	}
	a.persists++
	a.seg, a.next = seg, start
	return nil
}

// adapt 按上一段的使用情况调整下一段长度：发完则翻倍（封顶），
// 到期未发完则减半（保底）。
func (a *Allocator) adapt(now time.Time) {
	if a.seg.Len == 0 {
		return
	}
	switch {
	case a.next >= a.seg.End() && now.Before(a.seg.Expiry):
		a.segLen = min(a.segLen*2, a.cfg.MaxSeg)
	case !a.seg.ValidAt(now) && a.next < a.seg.End():
		a.segLen = max(a.segLen/2, a.cfg.MinSeg)
	}
}

// PersistWrites 返回持久写次数。
func (a *Allocator) PersistWrites() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.persists
}

// SegLen 返回当前段长。
func (a *Allocator) SegLen() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.segLen
}
