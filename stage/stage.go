// Package stage 提供管线阶段的通用骨架：有界队列、背压、优雅停止。
package stage

import (
	"context"
	"sync/atomic"
)

// Length 由可统计在途长度的队列实现。
type Length interface {
	Len() int
}

// Queue 是有界通道。满时 Send 阻塞并记录一次阻塞，背压由此传向上游。
type Queue[T any] struct {
	ch      chan T
	blocked int64
}

// NewQueue 创建容量为 cap（>=1）的有界队列。
func NewQueue[T any](cap int) *Queue[T] {
	return &Queue[T]{ch: make(chan T, cap)}
}

// Ch 返回底层通道供消费方 range。
func (q *Queue[T]) Ch() <-chan T { return q.ch }

// Len 返回当前在途元素数。
func (q *Queue[T]) Len() int { return len(q.ch) }

// Send 在 ctx 取消时立即返回 false；入队前若已满，记一次阻塞。
func (q *Queue[T]) Send(ctx context.Context, v T) bool {
	if len(q.ch) == cap(q.ch) {
		atomic.AddInt64(&q.blocked, 1)
	}
	select {
	case q.ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// Blocked 返回历史阻塞次数。
func (q *Queue[T]) Blocked() int64 { return atomic.LoadInt64(&q.blocked) }

// Close 关闭队列，通知消费方不会再有元素。
func (q *Queue[T]) Close() { close(q.ch) }

// Gauge 记录若干队列「在途元素总和」的历史最大值。
type Gauge struct {
	queues []Length
	max    int
}

// NewGauge 绑定需要合计统计的队列。
func NewGauge(qs ...Length) *Gauge { return &Gauge{queues: qs} }

// Sample 采样一次当前在途总和并更新历史最大值。
func (g *Gauge) Sample() {
	n := 0
	for _, q := range g.queues {
		n += q.Len()
	}
	if n > g.max {
		g.max = n
	}
}

// Max 返回历史最大在途数。
func (g *Gauge) Max() int { return g.max }

// Run 启动单个消费 goroutine，按 FIFO 处理 q 中全部元素直到 q 关闭。
// 优雅停止语义：生产方 Close 后，已在队列中的元素仍会被排空处理；
// consume 返回错误时停止处理新元素并把错误交回。
func Run[T any](ctx context.Context, q *Queue[T], consume func(context.Context, T) error) error {
	var first error
	for {
		select {
		case v, ok := <-q.Ch():
			if !ok {
				return first
			}
			if first != nil {
				continue
			}
			if err := consume(ctx, v); err != nil && first == nil {
				first = err
			}
		case <-ctx.Done():
			if first == nil {
				first = ctx.Err()
			}
			return first
		}
	}
}
