// Package stack 提供基础栈（LIFO），仅依赖标准库。
package stack

import "errors"

// 哨兵错误，可用 errors.Is 区分。
var (
	ErrPopEmpty  = errors.New("stack: pop on empty stack")
	ErrPeekEmpty = errors.New("stack: peek on empty stack")
)

// Stack 是元素类型为 T 的后进先出栈，零值即可用。
type Stack[T any] struct {
	items []T
}

// Push 将 v 压入栈顶。
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop 弹出栈顶；空栈返回 ErrPopEmpty。
func (s *Stack[T]) Pop() (T, error) {
	if len(s.items) == 0 {
		var zero T
		return zero, ErrPopEmpty
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, nil
}

// Peek 返回栈顶但不弹出；空栈返回 ErrPeekEmpty。
func (s *Stack[T]) Peek() (T, error) {
	if len(s.items) == 0 {
		var zero T
		return zero, ErrPeekEmpty
	}
	return s.items[len(s.items)-1], nil
}

// Len 返回栈中元素个数。
func (s *Stack[T]) Len() int {
	return len(s.items)
}
