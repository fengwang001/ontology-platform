// Package stack 提供基础栈（LIFO）。
package stack

import (
	"errors"
	"fmt"
)

// 三类哨兵错误，可用 errors.Is 区分。
var (
	// ErrEmpty 是空栈错误的根因。
	ErrEmpty = errors.New("stack: empty")
	// ErrPop 表示对空栈 Pop。
	ErrPop = fmt.Errorf("pop: %w", ErrEmpty)
	// ErrPeek 表示对空栈 Peek。
	ErrPeek = fmt.Errorf("peek: %w", ErrEmpty)
)

// Stack 是后进先出栈。
type Stack[T any] struct {
	items []T
}

// Push 把 v 压入栈顶。
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop 弹出栈顶；空栈返回 ErrPop。
func (s *Stack[T]) Pop() (T, error) {
	if len(s.items) == 0 {
		var zero T
		return zero, ErrPop
	}
	v := s.items[len(s.items)-1]
	var zero T
	s.items[len(s.items)-1] = zero
	s.items = s.items[:len(s.items)-1]
	return v, nil
}

// Peek 返回栈顶但不弹出；空栈返回 ErrPeek。
func (s *Stack[T]) Peek() (T, error) {
	if len(s.items) == 0 {
		var zero T
		return zero, ErrPeek
	}
	return s.items[len(s.items)-1], nil
}

// Len 返回栈中元素个数。
func (s *Stack[T]) Len() int {
	return len(s.items)
}
