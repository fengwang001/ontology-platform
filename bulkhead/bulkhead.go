// Package bulkhead 为单个下游提供并发额度 + 有界等待队列的舱壁隔离。
// 不同下游各持有一个实例，额度互不影响。
package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// ErrFull 表示在途额度与等待队列均已满，请求被立即拒绝。
var ErrFull = errors.New("bulkhead: in-flight slots and wait queue are full")

// Bulkhead 是单个下游的舱壁。并发安全。
type Bulkhead struct {
	name  string
	slots chan struct{} // 容量 = 并发额度 N
	queue chan struct{} // 容量 = 等待队列上限 Q
	peak  atomic.Int64  // 非导出历史峰值计数器
}

// New 创建一个舱壁：size 为并发额度 N，queue 为等待队列上限 Q。
// size <= 0 或 queue < 0 时返回错误。
func New(name string, size, queue int) (*Bulkhead, error) {
	if size <= 0 {
		return nil, fmt.Errorf("bulkhead %q: size must be > 0, got %d", name, size)
	}
	if queue < 0 {
		return nil, fmt.Errorf("bulkhead %q: queue must be >= 0, got %d", name, queue)
	}
	return &Bulkhead{
		name:  name,
		slots: make(chan struct{}, size),
		queue: make(chan struct{}, queue),
	}, nil
}

// Acquire 获取一个并发额度，返回必须调用一次的归还函数。
// 在途未满时立即成功；否则在等待队列有空位时排队，直到拿到额度或
// ctx 取消（取消不持有名额）；队列满时立即返回 ErrFull，绝不阻塞。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case b.slots <- struct{}{}:
		b.track()
		return b.release, nil
	default:
	}
	select {
	case b.queue <- struct{}{}:
	default:
		return nil, ErrFull
	}
	defer func() { <-b.queue }()
	select {
	case b.slots <- struct{}{}:
		b.track()
		return b.release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Bulkhead) release() { <-b.slots }

func (b *Bulkhead) track() {
	n := int64(len(b.slots))
	for {
		p := b.peak.Load()
		if n <= p || b.peak.CompareAndSwap(p, n) {
			return
		}
	}
}

// Name 返回舱壁名（下游标识）。
func (b *Bulkhead) Name() string { return b.name }

// InFlight 返回当前在途数，恒不超过额度 N。
func (b *Bulkhead) InFlight() int { return len(b.slots) }

// Waiting 返回当前排队等待数，恒不超过队列上限 Q。
func (b *Bulkhead) Waiting() int { return len(b.queue) }

// Available 返回当前可用额度。
func (b *Bulkhead) Available() int { return cap(b.slots) - len(b.slots) }

// Peak 返回历史在途峰值（只读访问非导出计数器）。
func (b *Bulkhead) Peak() int64 { return b.peak.Load() }
