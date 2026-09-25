// Package bulkhead 实现舱壁隔离：每个下游独立的并发额度与等待队列上限。
package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/classify"
)

// ErrRejected 表示并发额度与等待队列均已满，请求被立即拒绝。
var ErrRejected = errors.New("bulkhead: rejected")

// RejectedError 携带拒绝时刻（取自注入时钟），可用 errors.Is 命中 ErrRejected。
type RejectedError struct {
	At time.Time
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("bulkhead: rejected at %s", e.At.Format(time.RFC3339Nano))
}

func (e *RejectedError) Is(target error) bool { return target == ErrRejected }

// Config 是舱壁配置。
type Config struct {
	Concurrency int              // 同时在途的真实调用上限 N，必须 > 0
	Queue       int              // 等待队列上限 Q，第 N+Q+1 个请求立即被拒
	Now         func() time.Time // 注入时钟，nil 用 time.Now
}

// Bulkhead 是单个下游的舱壁。不同下游各持一个实例即天然隔离。
type Bulkhead struct {
	tokens chan struct{}
	now    func() time.Time

	mu       sync.Mutex
	waiting  int // 已登记的等待名额
	queueMax int
	inflight int
	peak     int // 非导出历史峰值，仅持锁更新
}

// New 构造舱壁；Concurrency <= 0 或 Queue < 0 报错。
func New(cfg Config) (*Bulkhead, error) {
	if cfg.Concurrency <= 0 {
		return nil, fmt.Errorf("bulkhead: concurrency must be > 0, got %d", cfg.Concurrency)
	}
	if cfg.Queue < 0 {
		return nil, fmt.Errorf("bulkhead: queue must be >= 0, got %d", cfg.Queue)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Bulkhead{
		tokens:   make(chan struct{}, cfg.Concurrency),
		now:      now,
		queueMax: cfg.Queue,
	}, nil
}

// Acquire 获取一份并发额度，返回归还函数。
// 额度满且队列满时立即返回 *RejectedError；等待中 ctx 取消则归还等待名额。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	select {
	case b.tokens <- struct{}{}:
		b.enter()
		return b.release, nil
	default:
	}
	b.mu.Lock()
	if b.waiting >= b.queueMax {
		b.mu.Unlock()
		return nil, &RejectedError{At: b.now()}
	}
	b.waiting++
	b.mu.Unlock()
	select {
	case b.tokens <- struct{}{}:
		b.leaveQueue()
		b.enter()
		return b.release, nil
	case <-ctx.Done():
		b.leaveQueue()
		return nil, ctx.Err()
	}
}

// Execute 在额度保护下执行 fn。成功、失败、超时、panic 四条路径都归还额度；
// panic 被捕获并转为包裹 classify.ErrPanic 的错误。
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) (err error) {
	release, err := b.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", classify.ErrPanic, r)
		}
	}()
	return fn()
}

func (b *Bulkhead) enter() {
	b.mu.Lock()
	b.inflight++
	if b.inflight > b.peak {
		b.peak = b.inflight
	}
	b.mu.Unlock()
}

func (b *Bulkhead) leaveQueue() {
	b.mu.Lock()
	b.waiting--
	b.mu.Unlock()
}

func (b *Bulkhead) release() {
	<-b.tokens
	b.mu.Lock()
	b.inflight--
	b.mu.Unlock()
}

// InFlight 返回当前在途真实调用数。
func (b *Bulkhead) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inflight
}

// Peak 返回历史峰值在途数，恒不超过 Concurrency。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Available 返回当前可用额度。
func (b *Bulkhead) Available() int {
	return cap(b.tokens) - len(b.tokens)
}
