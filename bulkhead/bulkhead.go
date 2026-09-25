// Package bulkhead 为每个下游提供独立的并发额度与有界等待队列。
package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrBulkheadFull 表示额度与等待队列均已满，请求被立即拒绝。
var ErrBulkheadFull = errors.New("bulkhead full")

// Bulkhead 是单个下游的舱壁：N 个并发额度 + Q 个等待名额。
type Bulkhead struct {
	sem   chan struct{} // 并发额度，容量 N
	queue chan struct{} // 等待名额，容量 Q

	mu       sync.Mutex
	inFlight int
	peak     int // 历史峰值，非导出计数器
}

// New 构造舱壁；n 为并发额度（>=1），q 为等待队列上限（>=0）。
func New(n, q int) (*Bulkhead, error) {
	if n < 1 {
		return nil, fmt.Errorf("bulkhead: concurrency %d < 1", n)
	}
	if q < 0 {
		return nil, fmt.Errorf("bulkhead: queue %d < 0", q)
	}
	return &Bulkhead{
		sem:   make(chan struct{}, n),
		queue: make(chan struct{}, q),
	}, nil
}

// Acquire 获取一个额度，返回归还函数。队列满立即返回 ErrBulkheadFull；
// 等待中 ctx 取消则离开队列并返回 ctx.Err()。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	select {
	case b.sem <- struct{}{}:
		b.enter()
		return b.release, nil
	default:
	}
	select {
	case b.queue <- struct{}{}:
	default:
		return nil, ErrBulkheadFull
	}
	select {
	case b.sem <- struct{}{}:
		<-b.queue
		b.enter()
		return b.release, nil
	case <-ctx.Done():
		<-b.queue
		return nil, ctx.Err()
	}
}

func (b *Bulkhead) enter() {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.peak {
		b.peak = b.inFlight
	}
	b.mu.Unlock()
}

func (b *Bulkhead) release() {
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	<-b.sem
}

// InFlight 返回当前在途调用数。
func (b *Bulkhead) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

// Peak 返回历史在途峰值。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Available 返回当前可用额度。
func (b *Bulkhead) Available() int {
	return cap(b.sem) - len(b.sem)
}
