// Package bulkhead 为单个下游提供并发额度与等待队列上限的舱壁隔离。
package bulkhead

import (
	"context"
	"errors"
	"sync"

	"ontology/classify"
)

// Bulkhead 是一个下游独占的舱壁，零值不可用，须用 New 构造。
type Bulkhead struct {
	mu       sync.Mutex
	cond     *sync.Cond
	capacity int
	queue    int
	inflight int
	peak     int // 非导出：历史在途峰值
	waiters  []*waiter
}

type waiter struct {
	granted  bool
	canceled bool // ctx 已取消
	done     bool // 已被出队（授予或跳过）
}

// New 构造舱壁：capacity 为并发额度（>0），queue 为等待队列上限（>=0）。
func New(capacity, queue int) (*Bulkhead, error) {
	if capacity <= 0 {
		return nil, errors.New("bulkhead: capacity must be > 0")
	}
	if queue < 0 {
		return nil, errors.New("bulkhead: queue must be >= 0")
	}
	b := &Bulkhead{capacity: capacity, queue: queue}
	b.cond = sync.NewCond(&b.mu)
	return b, nil
}

// Acquire 尝试获取一个在途名额。
// 在途满且等待队列也满时立即返回 ErrBulkheadRejected；ctx 取消则放弃排队。
// 返回的 release 必须且仅需调用一次，任何结果路径都应 defer。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	b.mu.Lock()
	if b.inflight < b.capacity {
		b.grant()
	b.mu.Unlock()
		return b.onceRelease(), nil
	}
	if len(b.waiters) >= b.queue {
		b.mu.Unlock()
		return nil, classify.ErrBulkheadRejected
	}
	w := &waiter{}
	b.waiters = append(b.waiters, w)
	stop := make(chan struct{})
	if d := ctx.Done(); d != nil {
		go func() {
			select {
			case <-d:
				b.mu.Lock()
				w.canceled = true
				b.cond.Broadcast()
				b.mu.Unlock()
			case <-stop:
			}
		}()
	}
	for {
		b.cond.Wait()
		if w.granted {
			close(stop)
			b.mu.Unlock()
			return b.onceRelease(), nil
		}
		if w.done {
			close(stop)
			b.mu.Unlock()
			return nil, ctx.Err()
		}
		if w.canceled && len(b.waiters) > 0 && w == b.waiters[0] {
			b.waiters = b.waiters[1:]
			w.done = true
			b.cond.Broadcast()
			close(stop)
			b.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// 调用方持锁。
func (b *Bulkhead) grant() {
	b.inflight++
	if b.inflight > b.peak {
		b.peak = b.inflight
	}
}

func (b *Bulkhead) onceRelease() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.inflight--
			for len(b.waiters) > 0 {
				head := b.waiters[0]
				b.waiters = b.waiters[1:]
				if head.canceled {
					head.done = true
					b.cond.Broadcast()
					continue // 队首等待者已取消，跳过并继续寻找下一个
				}
				head.granted = true
				b.grant()
				break
			}
			b.cond.Broadcast()
			b.mu.Unlock()
		})
	}
}

// Available 返回当前剩余在途名额。
func (b *Bulkhead) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity - b.inflight
}

// Peak 返回历史在途峰值。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Waiting 返回当前队列中的等待者数。
func (b *Bulkhead) Waiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.waiters)
}
