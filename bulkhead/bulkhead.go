// Package bulkhead 实现舱壁隔离：每个下游独立的并发额度与等待队列上限。
// 额度在成功、失败、超时、panic 四条路径上都通过 defer 归还，不会泄漏。
package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrFull 表示并发额度与等待队列均已满，请求被立即拒绝。
var ErrFull = errors.New("bulkhead: full")

// Bulkhead 是单个下游的舱壁。零值不可用，须用 New 构造。
type Bulkhead struct {
	sem   chan struct{} // 并发额度，容量 N
	queue chan struct{} // 等待队列名额，容量 Q

	mu   sync.Mutex
	in   int // 当前在途数
	peak int // 历史在途峰值（非导出，供内部测试断言）
}

// New 创建额度为 n、等待队列上限为 q 的舱壁。n 必须 ≥ 1，q 必须 ≥ 0。
func New(n, q int) (*Bulkhead, error) {
	if n < 1 {
		return nil, fmt.Errorf("bulkhead: permits %d < 1", n)
	}
	if q < 0 {
		return nil, fmt.Errorf("bulkhead: queue %d < 0", q)
	}
	return &Bulkhead{sem: make(chan struct{}, n), queue: make(chan struct{}, q)}, nil
}

// Acquire 获取一份额度。无空闲额度时进入等待队列；队列已满则立即返回
// ErrFull；等待中 ctx 取消则离开队列并返回 ctx 错误，名额随之归还。
func (b *Bulkhead) Acquire(ctx context.Context) error {
	select {
	case b.sem <- struct{}{}:
		b.track()
		return nil
	default:
	}
	select {
	case b.queue <- struct{}{}:
	default:
		return ErrFull
	}
	defer func() { <-b.queue }()
	select {
	case b.sem <- struct{}{}:
		b.track()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release 归还一份额度，必须与成功的 Acquire 配对。
// 计数减量先于通道接收，保证在途计数恒不超过额度。
func (b *Bulkhead) Release() {
	b.mu.Lock()
	b.in--
	b.mu.Unlock()
	<-b.sem
}

// Execute 获取额度后执行 fn，并在所有路径（成功/失败/超时/panic）上归还
// 额度。fn 内 panic 被 recover 转为错误返回，不会击穿包装器。
func (b *Bulkhead) Execute(ctx context.Context, fn func(context.Context) error) (err error) {
	if err := b.Acquire(ctx); err != nil {
		return err
	}
	defer b.Release()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bulkhead: panic recovered: %v", r)
		}
	}()
	return fn(ctx)
}

// Available 返回当前空闲额度数。
func (b *Bulkhead) Available() int { return cap(b.sem) - len(b.sem) }

func (b *Bulkhead) track() {
	b.mu.Lock()
	b.in++
	if b.in > b.peak {
		b.peak = b.in
	}
	b.mu.Unlock()
}

func (b *Bulkhead) peakInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}
