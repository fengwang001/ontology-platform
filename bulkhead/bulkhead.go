// Package bulkhead 为每个下游提供独立的在途并发额度与等待队列上限。
package bulkhead

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrBulkheadRejected 表示在途额度与等待位均已满，请求被立即拒绝。
	ErrBulkheadRejected = errors.New("bulkhead: rejected, concurrency limit reached")
	// ErrInvalidConfig 表示构造参数非法。
	ErrInvalidConfig = errors.New("bulkhead: concurrency and queue must be positive")
)

type waiter struct{ ch chan struct{} }

// Bulkhead 限制单个下游的在途调用数与等待数。
type Bulkhead struct {
	mu      sync.Mutex
	running int
	queue   []*waiter
	maxRun  int
	maxQ    int
	peak    int
}

// New 构造舱壁；concurrency 与 queue 都必须为正。
func New(concurrency, queue int) (*Bulkhead, error) {
	if concurrency <= 0 || queue <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Bulkhead{maxRun: concurrency, maxQ: queue}, nil
}

// Acquire 取得一个在途额度。有空位立即返回；否则在等待位内排队，
// 排队期间 ctx 取消则退出队列并返回 ctx 错误；等待位满则立即返回
// ErrBulkheadRejected（不等待）。
func (b *Bulkhead) Acquire(ctx context.Context) error {
	b.mu.Lock()
	if b.running < b.maxRun {
		b.running++
		if b.running > b.peak {
			b.peak = b.running
		}
		b.mu.Unlock()
		return nil
	}
	if len(b.queue) >= b.maxQ {
		b.mu.Unlock()
		return ErrBulkheadRejected
	}
	w := &waiter{ch: make(chan struct{}, 1)}
	b.queue = append(b.queue, w)
	b.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		b.removeWaiter(w)
		return ctx.Err()
	}
}

func (b *Bulkhead) removeWaiter(w *waiter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, q := range b.queue {
		if q == w {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			return
		}
	}
}

// Release 归还一个在途额度，并把它交给队首等待者。
func (b *Bulkhead) Release() {
	b.mu.Lock()
	if b.running > 0 {
		b.running--
	}
	var head *waiter
	if len(b.queue) > 0 {
		head = b.queue[0]
		b.queue = b.queue[1:]
		b.running++
		if b.running > b.peak {
			b.peak = b.running
		}
	}
	b.mu.Unlock()
	if head != nil {
		head.ch <- struct{}{}
	}
}

// Running 返回当前在途数；Peak 返回历史峰值；Waiting 返回排队数。
func (b *Bulkhead) Running() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// Available 返回空闲额度数。
func (b *Bulkhead) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxRun - b.running
}

// Peak 返回历史上同时在途的最大值。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Waiting 返回当前排队中的请求数。
func (b *Bulkhead) Waiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}
