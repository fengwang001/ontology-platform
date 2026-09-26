// Package stage 提供管线阶段共用的有界队列、背压度量与优雅停止骨架。
package stage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrStopped 在队列因停止信号或 context 取消而无法继续收发时返回。
var ErrStopped = errors.New("stage: stopped")

// Gauge 统计当前与历史最大「已入队未出队」记录数。
// 任一记录在任一时刻只停留在一个队列中，因此当前值不超过各队列容量之和。
type Gauge struct {
	cur atomic.Int64
	max atomic.Int64
}

func NewGauge() *Gauge { return &Gauge{} }

func (g *Gauge) addLocked(delta int64) {
	cur := g.cur.Add(delta)
	if delta > 0 {
		for {
			m := g.max.Load()
			if cur <= m || g.max.CompareAndSwap(m, cur) {
				break
			}
		}
	}
}

// Current 返回当前在途记录数。
func (g *Gauge) Current() int64 { return g.cur.Load() }

// Max 返回历史最大在途记录数。
func (g *Gauge) Max() int64 { return g.max.Load() }

// Queue 是容量固定的有界队列：满时 Send 阻塞，压力直接传导给上游。
// 计数与入/出队在同一把锁内完成，因此 Gauge 当前值恒等于各队列长度之和，
// 历史最大值严格不超过各队列容量之和。
type Queue[T any] struct {
	mu       sync.Mutex
	nonFull  *sync.Cond
	nonEmpty *sync.Cond
	buf      []T
	cap      int
	closed   bool
	gauge    *Gauge
}

// NewQueue 创建容量为 cap 的有界队列；cap 至少为 1。
func NewQueue[T any](cap int, gauge *Gauge) *Queue[T] {
	if cap < 1 {
		cap = 1
	}
	q := &Queue[T]{cap: cap, gauge: gauge}
	q.nonFull = sync.NewCond(&q.mu)
	q.nonEmpty = sync.NewCond(&q.mu)
	return q
}

func waitCond(ctx context.Context, cond *sync.Cond) bool {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cond.Broadcast()
		case <-stop:
		}
	}()
	cond.Wait()
	close(stop)
	return ctx.Err() == nil
}

// TrySend 非阻塞尝试入队；满时返回 false，供上游统计背压阻塞次数。
func (q *Queue[T]) TrySend(v T) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, ErrStopped
	}
	if len(q.buf) >= q.cap {
		return false, nil
	}
	q.buf = append(q.buf, v)
	if q.gauge != nil {
		q.gauge.addLocked(1)
	}
	q.nonEmpty.Signal()
	return true, nil
}

// Send 在队列满时阻塞，直到入队成功或 ctx 取消。
func (q *Queue[T]) Send(ctx context.Context, v T) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) >= q.cap && !q.closed {
		if !waitCond(ctx, q.nonFull) {
			return ErrStopped
		}
	}
	if q.closed {
		return ErrStopped
	}
	q.buf = append(q.buf, v)
	if q.gauge != nil {
		q.gauge.addLocked(1)
	}
	q.nonEmpty.Signal()
	return nil
}

// Recv 阻塞出队；ok 为 false 表示队列已关闭且排空。
func (q *Queue[T]) Recv(ctx context.Context) (v T, ok bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 && !q.closed {
		if !waitCond(ctx, q.nonEmpty) {
			return v, false, ErrStopped
		}
	}
	if len(q.buf) == 0 {
		return v, false, nil
	}
	v = q.buf[0]
	q.buf = q.buf[1:]
	if q.gauge != nil {
		q.gauge.addLocked(-1)
	}
	q.nonFull.Signal()
	return v, true, nil
}

// Close 关闭队列，允许接收方排空剩余元素后自然退出。
func (q *Queue[T]) Close() {
	q.mu.Lock()
	q.closed = true
	q.nonFull.Broadcast()
	q.nonEmpty.Broadcast()
	q.mu.Unlock()
}

// Group 归并一组阶段 goroutine 的生命周期，支持幂等停止与等待。
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

func NewGroup(parent context.Context) *Group {
	ctx, cancel := context.WithCancel(parent)
	return &Group{ctx: ctx, cancel: cancel}
}

func (g *Group) Ctx() context.Context { return g.ctx }

// Go 启动一个随组生命周期管理的 worker。
func (g *Group) Go(fn func(ctx context.Context)) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		fn(g.ctx)
	}()
}

// Stop 幂等取消组内所有 worker。
func (g *Group) Stop() { g.once.Do(g.cancel) }

// Wait 等待全部 worker 退出，杜绝悬挂 goroutine。
func (g *Group) Wait() { g.wg.Wait() }
