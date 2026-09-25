// Package ring 提供定长环形缓冲区：满时拒绝写入而非覆盖。
package ring

import (
	"errors"
	"sync"
)

var (
	ErrBadCap = errors.New("ring: capacity must be positive")
	ErrFull   = errors.New("ring: buffer is full")
)

// Buffer 是固定容量的 FIFO 环形缓冲区，可安全并发使用。
type Buffer[T any] struct {
	mu   sync.Mutex
	buf  []T
	r    int // 读指针
	w    int // 写指针
	size int // 当前元素数，用于区分指针重合时的空/满
	max  int // 历史最大暂存数
}

// New 创建容量为 cap 的缓冲区；cap <= 0 返回 ErrBadCap。
func New[T any](cap int) (*Buffer[T], error) {
	if cap <= 0 {
		return nil, ErrBadCap
	}
	return &Buffer[T]{buf: make([]T, cap)}, nil
}

// Enqueue 写入一个元素；缓冲满时返回 ErrFull 且状态不变。
func (b *Buffer[T]) Enqueue(v T) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == len(b.buf) {
		return ErrFull
	}
	b.buf[b.w] = v
	b.w = (b.w + 1) % len(b.buf)
	b.size++
	if b.size > b.max {
		b.max = b.size
	}
	return nil
}

// Dequeue 弹出一个元素；空时返回 (零值, false) 且状态不变。
func (b *Buffer[T]) Dequeue() (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var zero T
	if b.size == 0 {
		return zero, false
	}
	v := b.buf[b.r]
	b.buf[b.r] = zero
	b.r = (b.r + 1) % len(b.buf)
	b.size--
	return v, true
}

func (b *Buffer[T]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

func (b *Buffer[T]) Cap() int { return len(b.buf) }

func (b *Buffer[T]) Full() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == len(b.buf)
}

func (b *Buffer[T]) Empty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == 0
}

// MaxLen 返回历史最大暂存数，用于验证内存占用上界。
func (b *Buffer[T]) MaxLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.max
}
