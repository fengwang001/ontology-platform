// Package link 实现单条有向复制链路上的 FIFO 队列、积压预算与投递故障。
package link

import (
	"container/list"
	"errors"
	"sync"

	"ontology/region"
)

// ErrNotFound 标识不在失败列表中。
var ErrNotFound = errors.New("失败项不存在")

// Item 链路上的一个复制项。
type Item struct {
	ID     region.VersionID
	Key    string
	Size   int64
	TS     int64
	Marker bool
	// Attempts 为进入失败列表前已消耗的失败次数（达到 R）；重新入队后清零。
	Attempts int
}

// Link 单条有向链路。方法均为线程安全的。
type Link struct {
	mu sync.Mutex

	capacity   int64
	retryLimit int

	queue   *list.List // *entry，值为 Item
	backlog int64
	failed  map[region.VersionID]Item

	enqueued        int64
	delivered       int64
	failedTransfers int64
	lost            int64
}

type entry struct{ it Item }

// New 创建链路：capacity 为积压字节上限，retryLimit 为重试上限 R。
func New(capacity int64, retryLimit int) *Link {
	return &Link{
		capacity:   capacity,
		retryLimit: retryLimit,
		queue:      list.New(),
		failed:     make(map[region.VersionID]Item),
	}
}

// Enqueue 将新项放入队尾；backlog+size>capacity 时返回 false 且不改变任何状态。
func (l *Link) Enqueue(it Item) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.backlog+it.Size > l.capacity {
		return false
	}
	l.enqueueLocked(it)
	return true
}

// DeliverN 从队首起至多调用 n 次 send；send 返回后按规则决定 Apply、出队、重试或失败转移。
func (l *Link) DeliverN(n int, send func(Item) (applied bool, err error), applyDst func(Item) (duplicate bool)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := 0; i < n; i++ {
		front := l.queue.Front()
		if front == nil {
			return
		}
		en := front.Value.(*entry)
		applied, err := send(en.it)
		if applied || err == nil {
			applyDst(en.it) // applied==true 且 err!=nil：确认丢失，仍 Apply。
		}
		if err == nil {
			l.removeFrontLocked(front)
			l.delivered++
			continue
		}
		en.it.Attempts++
		if en.it.Attempts < l.retryLimit {
			return // 保序阻塞：队首仍在队，本次 Deliver 结束。
		}
		// 达到 R：失败转移——出队、释放积压、进入失败列表，继续处理后续项。
		l.removeFrontLocked(front)
		l.failedTransfers++
		l.failed[en.it.ID] = en.it
	}
}

// Retry 把失败项移回队尾并清零重试计数；失败列表无此标识返回 found=false，
// 预算不足返回 ok=false（且不改变状态）。
func (l *Link) Retry(id region.VersionID) (ok bool, found bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	it, ok := l.failed[id]
	if !ok {
		return false, false
	}
	if l.backlog+it.Size > l.capacity {
		return false, true
	}
	delete(l.failed, id)
	it.Attempts = 0
	l.enqueueLocked(it)
	return true, true
}

// Backlog 当前在队项字节之和。
func (l *Link) Backlog() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.backlog
}

// Pending 当前在队项数。
func (l *Link) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queue.Len()
}

// Failed 返回失败列表中各项的快照。
func (l *Link) Failed() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Item, 0, len(l.failed))
	for _, it := range l.failed {
		out = append(out, it)
	}
	return out
}

// DumpQueue 返回在队项的 FIFO 顺序快照（供测试核对状态）。
func (l *Link) DumpQueue() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Item, 0, l.queue.Len())
	for e := l.queue.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*entry).it)
	}
	return out
}

// Enqueued 累计入队次数（含 Retry 重新入队）。
func (l *Link) Enqueued() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enqueued
}

// Delivered 累计成功出队（已投递）次数。
func (l *Link) Delivered() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.delivered
}

// FailedTransfers 累计失败转移次数。
func (l *Link) FailedTransfers() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failedTransfers
}

// Lost Relaxed 模式下该链路跳过入队的累计次数。
func (l *Link) Lost() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lost
}

// AddLost 记录一次 Relaxed 丢失。
func (l *Link) AddLost() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lost++
}

// 以下方法要求调用方持有 l.mu。

func (l *Link) enqueueLocked(it Item) {
	l.queue.PushBack(&entry{it: it})
	l.backlog += it.Size
	l.enqueued++
}

func (l *Link) removeFrontLocked(e *list.Element) {
	l.queue.Remove(e)
	l.backlog -= e.Value.(*entry).it.Size
}
