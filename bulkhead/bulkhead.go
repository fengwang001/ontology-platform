// Package bulkhead 为每个下游提供独立的并发额度与等待队列上限。
package bulkhead

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrFull 表示并发额度与等待队列都已占满，请求被立即拒绝。
var ErrFull = errors.New("bulkhead: concurrency and queue limits reached")

// Bulkhead 用两个 channel 信号量实现：slots 容量为并发额度 N，
// queue 容量为等待队列上限 Q。第 N+Q+1 个请求立即得到 ErrFull。
type Bulkhead struct {
	slots chan struct{}
	queue chan struct{}
	now   func() time.Time

	mu         sync.Mutex
	peak       int
	lastReject time.Time
}

// New 构造舱壁：n 为并发额度（必须为正），q 为等待队列上限（可为 0），
// now 为注入时钟（nil 则用真实时钟），用于给拒绝打时间戳。
func New(n, q int, now func() time.Time) (*Bulkhead, error) {
	if n <= 0 {
		return nil, errors.New("bulkhead: concurrency limit must be positive")
	}
	if q < 0 {
		return nil, errors.New("bulkhead: queue limit must not be negative")
	}
	if now == nil {
		now = time.Now
	}
	return &Bulkhead{
		slots: make(chan struct{}, n),
		queue: make(chan struct{}, q),
		now:   now,
	}, nil
}

// Acquire 获取一份并发额度。额度占满但队列未满时排队等待，直到有额度
// 释放或 ctx 取消（取消时自动让出队列名额）；队列也满时立即返回
// ErrFull，不推进任何时钟。
func (b *Bulkhead) Acquire(ctx context.Context) error {
	select {
	case b.slots <- struct{}{}:
		b.observe()
		return nil
	default:
	}
	select {
	case b.queue <- struct{}{}:
	default:
		b.mu.Lock()
		b.lastReject = b.now()
		b.mu.Unlock()
		return ErrFull
	}
	defer func() { <-b.queue }()
	select {
	case b.slots <- struct{}{}:
		b.observe()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release 归还一份额度，必须与成功的 Acquire 一一配对。
func (b *Bulkhead) Release() {
	<-b.slots
}

// InFlight 返回当前在途的真实调用数，恒不超过 N。
func (b *Bulkhead) InFlight() int { return len(b.slots) }

// Waiting 返回当前排队等待的请求数，恒不超过 Q。
func (b *Bulkhead) Waiting() int { return len(b.queue) }

// Peak 返回历史在途峰值（内部计数器非导出，经此方法读取）。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// LastReject 返回最近一次因满员拒绝的时间（按注入时钟）。
func (b *Bulkhead) LastReject() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastReject
}

func (b *Bulkhead) observe() {
	b.mu.Lock()
	if n := len(b.slots); n > b.peak {
		b.peak = n
	}
	b.mu.Unlock()
}
