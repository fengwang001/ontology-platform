// Package stack 提供下标栈：压入、弹出、看栈顶、长度。不依赖其他包。
package stack

// Stack 是 int 下标栈，零值即可用。非并发安全，由调用方独占使用。
type Stack struct {
	items []int
}

// Push 把下标 i 压入栈顶。
func (s *Stack) Push(i int) {
	s.items = append(s.items, i)
}

// Pop 弹出栈顶下标；栈空时返回 ok=false。
func (s *Stack) Pop() (v int, ok bool) {
	n := len(s.items)
	if n == 0 {
		return 0, false
	}
	v = s.items[n-1]
	s.items = s.items[:n-1]
	return v, true
}

// Top 返回栈顶下标但不弹出；栈空时返回 ok=false。
func (s *Stack) Top() (v int, ok bool) {
	n := len(s.items)
	if n == 0 {
		return 0, false
	}
	return s.items[n-1], true
}

// Len 返回栈内元素个数。
func (s *Stack) Len() int {
	return len(s.items)
}
