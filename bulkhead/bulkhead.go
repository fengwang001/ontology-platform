// Package bulkhead 为单个下游提供并发额度与等待队列上限（舱壁隔离）。
package bulkhead

import (
	"context"
	"errors"
	"sync"
)

// ErrBulkheadFull 在在途额度与等待队列均满时立即返回。
var ErrBulkheadFull = errors.New("bulkhead: capacity and queue full")

// Config 是舱壁配置。
type Config struct {
	Name     string // 下游名；不同下游应使用不同实例
	Capacity int    // 同时在途真实调用上限 N
	Queue    int    // 等待队列上限 Q
}

type waiter struct {
	ch    chan struct{}
	alive bool
}

// Bulkhead 是单个下游的舱壁。
type Bulkhead struct {
	capacity int
	queue    int

	mu      sync.Mutex
	running int
	peak    int
	waiters []*waiter
}

// New 构造舱壁；非正额度返回错误。
func New(cfg Config) (*Bulkhead, error) {
	if cfg.Capacity <= 0 {
		return nil, errors.New("bulkhead: capacity must be positive")
	}
	if cfg.Queue < 0 {
		return nil, errors.New("bulkhead: queue must be non-negative")
	}
	return &Bulkhead{capacity: cfg.Capacity, queue: cfg.Queue}, nil
}

// Acquire 取得一个在途名额。满且队列满时立即返回 ErrBulkheadFull；
// ctx 取消时退出队列。返回的 release 必须且只需调用一次（幂等）。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	b.mu.Lock()
	if b.running < b.capacity {
		b.running++
		if b.running > b.peak {
			b.peak = b.running
		}
		b.mu.Unlock()
		return b.makeRelease(), nil
	}
	if len(b.waiters) >= b.queue {
		b.mu.Unlock()
		return nil, ErrBulkheadFull
	}
	w := &waiter{ch: make(chan struct{}, 1), alive: true}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	select {
	case <-w.ch:
		return b.makeRelease(), nil
	case <-ctx.Done():
		b.cancel(w)
		return nil, ctx.Err()
	}
}

// cancel 从等待队列摘除自己；若名额已在此刻发给本等待者，
// 则立即把名额转交给后续等待者，保证名额不泄漏。
func (b *Bulkhead) cancel(w *waiter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !w.alive {
		// 已被发放：ch 中有 token，代为持有并立刻归还。
		<-w.ch
		b.grantLocked()
		return
	}
	w.alive = false
}

func (b *Bulkhead) makeRelease() func() {
	var once sync.Once
	return func() {
		once.Do(b.putBack)
	}
}

func (b *Bulkhead) putBack() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.grantLocked()
}

// grantLocked 把一个名额交给队首存活等待者；无人可交则减少在途数。
func (b *Bulkhead) grantLocked() {
	for len(b.waiters) > 0 {
		w := b.waiters[0]
		b.waiters = b.waiters[1:]
		if !w.alive {
			continue
		}
		w.alive = false
		w.ch <- struct{}{} // cap=1，锁内不会阻塞
		return
	}
	b.running--
}

// Running 返回当前在途真实调用数。
func (b *Bulkhead) Running() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// Available 返回当前空闲名额。
func (b *Bulkhead) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity - b.running
}

// Peak 返回历史在途峰值。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}
