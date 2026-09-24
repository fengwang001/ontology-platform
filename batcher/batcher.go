// Package batcher 只负责攒批：条数上限、字节上限、等待时长三者先到即触发。
// 它不碰磁盘、不分配序号，时钟通过 after 函数注入以便测试。
package batcher

import (
	"time"

	"ontology/req"
)

// Batcher 是非并发安全的攒批器，由单一路径（组提交 leader）串行使用。
type Batcher struct {
	maxCount int
	maxBytes int
	maxWait  time.Duration
	after    func(time.Duration) <-chan time.Time

	items []req.Request
	bytes int
	timer <-chan time.Time
}

// New 创建攒批器。maxCount>=1、maxBytes>=1；after 为 nil 时用 time.After。
func New(maxCount, maxBytes int, maxWait time.Duration,
	after func(time.Duration) <-chan time.Time) *Batcher {
	if after == nil {
		after = time.After
	}
	return &Batcher{
		maxCount: maxCount,
		maxBytes: maxBytes,
		maxWait:  maxWait,
		after:    after,
	}
}

// Len 返回当前批条数。
func (b *Batcher) Len() int { return len(b.items) }

// Bytes 返回当前批 payload 总字节数。
func (b *Batcher) Bytes() int { return b.bytes }

// Timer 返回当前批的超时 channel；批为空时为 nil（select 中永不就绪）。
// 计时从批内第一条到达开始。
func (b *Batcher) Timer() <-chan time.Time { return b.timer }

// FlushFirst 报告加入 r 之前是否必须先冲刷当前批：
// 当前批非空，且加入 r 会使条数或字节数达到/超过上限。
func (b *Batcher) FlushFirst(payloadLen int) bool {
	if len(b.items) == 0 {
		return false
	}
	return len(b.items) >= b.maxCount ||
		(b.maxBytes > 0 && b.bytes+payloadLen > b.maxBytes)
}

// Add 把请求收入当前批；第一条到达时启动等待计时。
func (b *Batcher) Add(r req.Request) {
	if len(b.items) == 0 {
		b.timer = b.after(b.maxWait)
	}
	b.items = append(b.items, r)
	b.bytes += len(r.Payload)
}

// Full 报告加入后当前批是否应立即冲刷：条数达上限，或字节达上限。
// 单条超过字节上限时，当前批只有它一条，因此它单独成批而不被拒绝。
func (b *Batcher) Full() bool {
	return len(b.items) >= b.maxCount ||
		(b.maxBytes > 0 && b.bytes >= b.maxBytes)
}

// Drain 取出当前批并重置攒批器；空批返回 nil。
func (b *Batcher) Drain() []req.Request {
	if len(b.items) == 0 {
		return nil
	}
	out := b.items
	b.items = nil
	b.bytes = 0
	b.timer = nil
	return out
}
