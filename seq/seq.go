package seq

import "ontology/ring"

var (
	ErrBadCap = ring.ErrBadCap
	ErrFull   = ring.ErrFull
	ErrEmpty  = ring.ErrEmpty
)

// Buffer 是有界转发缓冲在 seq 层的别名。
type Buffer[T any] = ring.Buffer[T]

// Producer 向缓冲投递一条上游产出。
type Producer[T any] func(b *Buffer[T], v T) error

// Consumer 从缓冲取出一条待转发结果。
type Consumer[T any] func(b *Buffer[T]) (T, bool)

// Send / Recv 是面向单生产者单消费者形态的默认实现。
func Send[T any](b *Buffer[T], v T) error { return b.Enqueue(v) }

func Recv[T any](b *Buffer[T]) (T, bool) { return b.Dequeue() }
