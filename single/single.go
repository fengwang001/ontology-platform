// Package single 实现回源单飞：同一键的并发执行合并为一次，
// 结果广播给全部等待者；失败不被缓存，可立即重试。
package single

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrTooManyInflight 表示在途执行数已达上限，本次调用被拒绝，
// 不合并、不启动新执行，也不改变任何已有状态。
var ErrTooManyInflight = errors.New("single: too many inflight executions")

type call[V any] struct {
	done    chan struct{}
	val     V
	err     error
	waiters atomic.Int64 // 合并等待者数（不含发起者）
}

// Group 是按键合并的单飞组，并发安全。零值不可用，须用 New 创建。
type Group[V any] struct {
	max   int // <=0 表示不限并发
	mu    sync.Mutex
	calls map[string]*call[V]
	count atomic.Int64 // fn 实际被调用的次数
}

// New 创建单飞组，maxConcurrent 为同时在途的执行数上限（<=0 不限）。
func New[V any](maxConcurrent int) *Group[V] {
	return &Group[V]{max: maxConcurrent, calls: make(map[string]*call[V])}
}

// Do 执行 fn 并按键合并：若同键执行已在途，则等待其完成并共享结果；
// 否则启动一次新执行。ctx 只控制本次等待，取消等待不影响在途执行
// 和其他等待者。fn 的结果不被缓存，返回后同键的下一次 Do 会重新执行。
func (g *Group[V]) Do(ctx context.Context, key string, fn func(context.Context) (V, error)) (V, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		c.waiters.Add(1)
		g.mu.Unlock()
		defer c.waiters.Add(-1)
		return wait(ctx, c)
	}
	if g.max > 0 && len(g.calls) >= g.max {
		g.mu.Unlock()
		var zero V
		return zero, ErrTooManyInflight
	}
	c := &call[V]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	g.count.Add(1)
	go func() {
		c.val, c.err = fn(ctx)
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		close(c.done)
	}()
	return wait(ctx, c)
}

func wait[V any](ctx context.Context, c *call[V]) (V, error) {
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

// Calls 返回 fn 实际被调用的总次数（合并后的等待不计入）。
func (g *Group[V]) Calls() int64 { return g.count.Load() }

// InFlight 报告指定键当前是否有在途执行。
func (g *Group[V]) InFlight(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.calls[key]
	return ok
}

// Saturated 报告当前是否已达并发上限（新键的执行将被拒绝）。
// 调用方可用它做零副作用的提前拒绝；它不影响 Do 自身的判定。
func (g *Group[V]) Saturated() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max > 0 && len(g.calls) >= g.max
}

// InFlightCount 返回当前在途执行数。
func (g *Group[V]) InFlightCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

// Waiters 返回指定键当前合并等待的调用者数（不含发起者）。
func (g *Group[V]) Waiters(key string) int64 {
	g.mu.Lock()
	c, ok := g.calls[key]
	g.mu.Unlock()
	if !ok {
		return 0
	}
	return c.waiters.Load()
}
