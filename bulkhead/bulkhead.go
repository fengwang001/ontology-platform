// Package bulkhead 提供每个下游独立的并发隔离：固定并发额度 N + 有界等待队列 Q。
package bulkhead

import (
	"context"
	"errors"
	"sync"
)

// ErrBulkheadFull 表示在途额度与等待队列均已满，请求被立即拒绝。
var ErrBulkheadFull = errors.New("bulkhead full")

// Bulkhead 是单下游的舱壁。零值不可用，须用 New 构造。
type Bulkhead struct {
	permits chan struct{}
	limit   int
	queue   chan struct{}

	mu       sync.Mutex
	inFlight int
	peak     int
}

// New 构造舱壁：limit 为并发额度，queue 为等待队列上限。二者必须为正数。
func New(limit, queue int) (*Bulkhead, error) {
	if limit <= 0 || queue <= 0 {
		return nil, errors.New("bulkhead: limit and queue must be positive")
	}
	return &Bulkhead{
		permits: make(chan struct{}, limit),
		limit:   limit,
		queue:   make(chan struct{}, queue),
	}, nil
}

// Acquire 获取一份额度。队列满时立即返回 ErrBulkheadFull；
// 等待期间 ctx 被取消则让出等待名额并返回 ctx.Err()。
func (b *Bulkhead) Acquire(ctx context.Context) error {
	select {
	case b.permits <- struct{}{}:
		b.enter()
		return nil
	default:
	}
	select {
	case b.queue <- struct{}{}:
	default:
		return ErrBulkheadFull
	}
	select {
	case b.permits <- struct{}{}:
		<-b.queue
		b.enter()
		return nil
	case <-ctx.Done():
		<-b.queue
		return ctx.Err()
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

// Release 归还一份额度。必须与成功的 Acquire 一一配对。
func (b *Bulkhead) Release() {
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	<-b.permits
}

// Available 返回当前剩余可用额度（含被等待者即将占用的部分）。
func (b *Bulkhead) Available() int { return b.limit - len(b.permits) }

// Peak 返回历史在途峰值（只读指标，供测试与监控）。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}
