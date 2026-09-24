// Package local 实现逐键本地缓冲：按条数阈值触发推送、热键标记与解除。
// 不依赖其他包。
package local

import "sort"

// Entry 是一个键在本地缓冲里的累计状态。
type Entry struct {
	Sum   int64 // 待推送 delta 之和
	Count int64 // 已攒事件条数
}

// Buffer 是两级维护的本地级。阈值触发时通过 map 直接定位单个键，
// 不做整表扫描；lastChecked 记录最近一次阈值推送检查过的键个数。
type Buffer struct {
	h           int64
	pend        map[string]Entry
	hot         map[string]bool
	lastChecked int64
}

// New 创建一个条数阈值为 h 的缓冲。h 由调用方（api 层）保证为正。
func New(h int64) *Buffer {
	return &Buffer{h: h, pend: map[string]Entry{}, hot: map[string]bool{}}
}

// Add 录入一条事件。若应推送，返回该键待推送 delta 之和与 true：
// 热键每条事件立即单独推送（不动既有缓冲）；非热键攒够 h 条触发推送，
// 缓冲清零并标记热键。否则返回 (0, false)。
func (b *Buffer) Add(key string, delta int64) (int64, bool) {
	if b.hot[key] {
		return delta, true
	}
	e := b.pend[key]
	e.Sum += delta
	e.Count++
	if e.Count >= b.h {
		delete(b.pend, key)
		b.hot[key] = true
		b.lastChecked = 1 // map 直接定位，只检查这一个键
		return e.Sum, true
	}
	b.pend[key] = e
	return 0, false
}

// MarkHot 把键标记为热键（批内重复判定用，不影响其缓冲）。
func (b *Buffer) MarkHot(key string) { b.hot[key] = true }

// Take 取出并清除该键的待推送和，同时把它移出热键集合。
func (b *Buffer) Take(key string) (int64, bool) {
	e, ok := b.pend[key]
	if ok {
		delete(b.pend, key)
	}
	delete(b.hot, key)
	return e.Sum, ok
}

// Pending 返回当前缓冲的副本（键 → 累计状态）。
func (b *Buffer) Pending() map[string]Entry {
	m := make(map[string]Entry, len(b.pend))
	for k, e := range b.pend {
		m[k] = e
	}
	return m
}

// Hot 按字典序返回当前热键集合。
func (b *Buffer) Hot() []string {
	s := make([]string, 0, len(b.hot))
	for k := range b.hot {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}
