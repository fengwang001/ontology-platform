// Package tick 持有 ticket 锁的两个原子计数器 next/serving，
// 只负责计数器的原子推进与「是否轮到本号」的判定，不依赖其他包。
package tick

import "sync/atomic"

// Counters 是 ticket 锁的全部共享状态：next 发出下一个号，serving 为当前服务号。
type Counters struct {
	next    atomic.Int64
	serving atomic.Int64

	// accessed 为非导出诊断量：最近一次取号/交接所访问的状态字（计数器）个数。
	// 仅供同包测试读取，绝不出现在任何导出接口中。
	accessed atomic.Int64
}

// New 创建两个计数器均为 0 的 ticket 状态。
func New() *Counters { return &Counters{} }

// Take 原子地 t:=next; next++，先到先得发号；只访问 next 一个状态字。
func (c *Counters) Take() int {
	t := c.next.Add(1) - 1
	c.accessed.Store(1)
	return int(t)
}

// IsServing 用一次原子读 serving 判定 t 是否正被服务；调用方不得缓存旧值。
func (c *Counters) IsServing(t int) bool { return c.serving.Load() == int64(t) }

// Serving 原子读当前正在被服务的号。
func (c *Counters) Serving() int { return int(c.serving.Load()) }

// Next 原子读已发出的下一个号（等待者数 = Next-Serving）。
func (c *Counters) Next() int { return int(c.next.Load()) }

// Handover 是 Release 的唯一状态推进：仅当 t==serving 时用 CAS 把 serving
// 原子地加 1；号不符立即返回 false 且不写任何计数器。CAS 循环保证并发的
// 重复/错号 Release 也至多推进一次，serving 只增 1、绝不跳号。
// 整个交接只访问 serving 一个状态字，与等待者数量无关（O(1) 交接）。
func (c *Counters) Handover(t int) bool {
	for {
		s := c.serving.Load()
		if s != int64(t) {
			return false
		}
		if c.serving.CompareAndSwap(s, s+1) {
			c.accessed.Store(1)
			return true
		}
	}
}

// TryTakeFree 仅在 next==serving（锁空闲、队列无人）时 CAS 占用号 n，
// 访问 serving、next 两个状态字；若有等待者则返回 false 且状态不变，
// 因而超时的 TryAcquire 从不在取号序列中留下空洞。
func (c *Counters) TryTakeFree() (int, bool) {
	for {
		n := c.next.Load()
		if n != c.serving.Load() {
			return 0, false
		}
		if c.next.CompareAndSwap(n, n+1) {
			c.accessed.Store(2)
			return int(n), true
		}
	}
}

// accessedWords 返回最近一次取号/交接访问的状态字个数（非导出，仅供包内测试）。
func (c *Counters) accessedWords() int64 { return c.accessed.Load() }
