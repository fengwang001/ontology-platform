// Package stack is a minimal stack of sequence indices.
// It depends on no other package of this module.
package stack

// Stack stores indices for a monotone scan.
type Stack struct {
	idx []int
}

// Push puts an index on top of the stack.
func (s *Stack) Push(i int) {
	s.idx = append(s.idx, i)
}

// Pop removes and returns the top index.
func (s *Stack) Pop() int {
	top := s.idx[len(s.idx)-1]
	s.idx = s.idx[:len(s.idx)-1]
	return top
}

// Top returns the top index without removing it.
func (s *Stack) Top() int {
	return s.idx[len(s.idx)-1]
}

// Len reports how many indices the stack holds.
func (s *Stack) Len() int {
	return len(s.idx)
}
