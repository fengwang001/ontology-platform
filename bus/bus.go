// Package bus 实现失效通知总线。
//
// 通知先入待投递队列（长度可配置上限），Flush 时按入队顺序
// 同步投递给所有订阅者。乱序注入由调用方以乱序 Publish 实现，
// 重复投递即重复 Publish，丢失用 DropPending 模拟。
package bus

import (
	"errors"
	"sync"

	"ontology/version"
)

// ErrQueueFull 表示待投递队列已满，本次入队被拒绝且未改变任何状态。
var ErrQueueFull = errors.New("bus: pending queue full")

// Notification 是一条失效通知：Key 对应的数据已推进到 Version。
type Notification struct {
	Key     string
	Version version.Version
}

// Bus 是失效通知总线，并发安全。
type Bus struct {
	mu       sync.Mutex
	capacity int // <=0 表示不限长
	pending  []Notification
	subs     []func(Notification)
}

// New 创建总线，capacity 为待投递队列长度上限（<=0 不限）。
func New(capacity int) *Bus {
	return &Bus{capacity: capacity}
}

// Subscribe 注册订阅者，Flush 时按注册顺序收到每条通知。
func (b *Bus) Subscribe(h func(Notification)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, h)
}

// Publish 入队一条通知。队列满时返回 ErrQueueFull 且不入队。
func (b *Bus) Publish(n Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity > 0 && len(b.pending) >= b.capacity {
		return ErrQueueFull
	}
	b.pending = append(b.pending, n)
	return nil
}

// PublishAll 原子地入队一批通知：要么全部入队，要么（超限时）
// 全部拒绝并返回 ErrQueueFull，已有队列内容不变。
func (b *Bus) PublishAll(ns []Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity > 0 && len(b.pending)+len(ns) > b.capacity {
		return ErrQueueFull
	}
	b.pending = append(b.pending, ns...)
	return nil
}

// DropPending 丢弃队首最多 k 条待投递通知（模拟通知丢失），
// 返回实际丢弃条数。
func (b *Bus) DropPending(k int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if k > len(b.pending) {
		k = len(b.pending)
	}
	if k < 0 {
		k = 0
	}
	b.pending = append([]Notification(nil), b.pending[k:]...)
	return k
}

// Flush 取出全部待投递通知并按入队顺序同步投递给所有订阅者。
// 投递期间入队的新通知留待下一次 Flush。
func (b *Bus) Flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	subs := append([]func(Notification){}, b.subs...)
	b.mu.Unlock()
	for _, n := range batch {
		for _, h := range subs {
			h(n)
		}
	}
}

// Pending 返回当前待投递通知条数。
func (b *Bus) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
