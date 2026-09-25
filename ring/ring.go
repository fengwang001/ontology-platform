package ring

import (
	"errors"
	"sync"
)

var (
	ErrBadCap = errors.New("ring: capacity must be positive")
	ErrFull   = errors.New("ring: buffer is full")
	ErrEmpty  = errors.New("ring: buffer is empty")
)

// Buffer 是定长环形缓冲区。满时报错而非覆盖旧数据。
// head 指向下一个待读出的槽位，tail 指向下一个待写入的槽位。
// 不允许单靠 head==tail 判空/满，用 size 消歧。
type Buffer[T any] struct {
	mu   sync.Mutex
	data []T
	head int
	tail int
	size int
	peak int
}

func New[T any](cap int) (*Buffer[T], error) {
	if cap <= 0 {
		return nil, ErrBadCap
	}
	return &Buffer[T]{data: make([]T, cap)}, nil
}

func (b *Buffer[T]) Enqueue(v T) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == len(b.data) {
		return ErrFull
	}
	b.data[b.tail] = v
	b.tail = (b.tail + 1) % len(b.data)
	b.size++
	if b.size > b.peak {
		b.peak = b.size
	}
	return nil
}

func (b *Buffer[T]) Dequeue() (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var zero T
	if b.size == 0 {
		return zero, false
	}
	v := b.data[b.head]
	b.data[b.head] = zero
	b.head = (b.head + 1) % len(b.data)
	b.size--
	return v, true
}

func (b *Buffer[T]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

func (b *Buffer[T]) Cap() int { return len(b.data) }

func (b *Buffer[T]) Full() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == len(b.data)
}

func (b *Buffer[T]) Empty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == 0
}

// PeakLen 返回历史最大暂存数，用于验证固定内存上界。
func (b *Buffer[T]) PeakLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}
