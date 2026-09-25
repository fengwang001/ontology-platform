// Package stack 提供泛型栈，是 queue 的底层数据结构。
package stack

import "errors"

var (
	// ErrEmpty 在对空栈执行 Pop/Peek 时返回。
	ErrEmpty = errors.New("stack: empty")
	// ErrNilReceiver 在未初始化的栈上调用方法时返回。
	ErrNilReceiver = errors.New("stack: nil receiver")
	// ErrBadCapacity 在 New 收到负容量时返回。
	ErrBadCapacity = errors.New("stack: negative capacity")
)

// Stack 是切片支持的 LIFO 栈，非并发安全，由上层加锁。
type Stack[T any] struct {
	data []T
}

// New 创建可预分配容量的栈；capacity 为负时返回哨兵错误。
func New[T any](capacity int) (*Stack[T], error) {
	if capacity < 0 {
		return nil, ErrBadCapacity
	}
	return &Stack[T]{data: make([]T, 0, capacity)}, nil
}

// Push 把 v 压入栈顶。
func (s *Stack[T]) Push(v T) error {
	if s == nil {
		return ErrNilReceiver
	}
	s.data = append(s.data, v)
	return nil
}

// Pop 弹出并返回栈顶；空栈返回零值与 ErrEmpty。
func (s *Stack[T]) Pop() (T, error) {
	var zero T
	if s == nil {
		return zero, ErrNilReceiver
	}
	if len(s.data) == 0 {
		return zero, ErrEmpty
	}
	v := s.data[len(s.data)-1]
	s.data[len(s.data)-1] = zero
	s.data = s.data[:len(s.data)-1]
	return v, nil
}

// Peek 返回栈顶但不弹出；空栈返回零值与 ErrEmpty。
func (s *Stack[T]) Peek() (T, error) {
	var zero T
	if s == nil {
		return zero, ErrNilReceiver
	}
	if len(s.data) == 0 {
		return zero, ErrEmpty
	}
	return s.data[len(s.data)-1], nil
}

// Len 返回栈内元素个数。
func (s *Stack[T]) Len() int {
	if s == nil {
		return 0
	}
	return len(s.data)
}
