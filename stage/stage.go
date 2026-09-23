// Package stage 提供有界队列 + 背压 + 优雅停止的通用阶段骨架。
package stage

import "context"

// Item 是队列中流动的任意消息（帧、屏障等）。
type Item interface{}

// Queue 是容量固定、带令牌背压的在途队列。
type Queue struct {
	ch chan Item
}

// NewQueue 创建容量为 cap 的队列。
func NewQueue(capacity int) *Queue { return &Queue{ch: make(chan Item, capacity)} }

// Send 在 ctx 取消前阻塞投递，记录在途与阻塞（骨架）。
func (q *Queue) Send(ctx context.Context, it Item) error { return nil }

// Done 声明一条在途记录处理完成（骨架）。
func (q *Queue) Done() {}

// Recv 取出一条消息（骨架）。
func (q *Queue) Recv(ctx context.Context) (Item, error) { return nil, context.Canceled }

// MaxInFlight 返回历史最大在途记录数（骨架）。
func (q *Queue) MaxInFlight() int64 { return 0 }

// Blocks 返回投递阻塞发生的次数（骨架）。
func (q *Queue) Blocks() int64 { return 0 }

// Close 关闭底层 channel。
func (q *Queue) Close() { close(q.ch) }
