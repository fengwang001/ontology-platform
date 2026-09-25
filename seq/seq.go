// Package seq wires producers and consumers to a shared ring buffer.
package seq

import (
	"errors"

	"ontology/ring"
)

var (
	ErrFull   = ring.ErrFull
	ErrBadCap = ring.ErrBadCap
	ErrEmpty  = errors.New("seq: buffer empty")
)

// Buffer is the shared queue between a Producer and a Consumer.
type Buffer[T any] = ring.Ring[T]

type Producer[T any] struct{ buf *ring.Ring[T] }

// Send enqueues v, returning ErrFull when the buffer is full.
func (p *Producer[T]) Send(v T) error { return p.buf.Enqueue(v) }

type Consumer[T any] struct{ buf *ring.Ring[T] }

// Recv dequeues the oldest element, or ErrEmpty when none is pending.
func (c *Consumer[T]) Recv() (T, error) {
	v, ok := c.buf.Dequeue()
	if !ok {
		return v, ErrEmpty
	}
	return v, nil
}

// Pair returns a producer/consumer pair sharing one buffer of size cap.
func Pair[T any](cap int) (*Producer[T], *Consumer[T], error) {
	buf, err := ring.New[T](cap)
	if err != nil {
		return nil, nil, err
	}
	return &Producer[T]{buf}, &Consumer[T]{buf}, nil
}
