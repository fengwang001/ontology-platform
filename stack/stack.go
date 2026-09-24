package stack

type Stack struct {
	items []int
}

func New() *Stack {
	return &Stack{items: make([]int, 0)}
}

func (s *Stack) Push(index int) {
	s.items = append(s.items, index)
}

func (s *Stack) Pop() (int, bool) {
	if len(s.items) == 0 {
		return 0, false
	}
	last := len(s.items) - 1
	index := s.items[last]
	s.items = s.items[:last]
	return index, true
}

func (s *Stack) Top() (int, bool) {
	if len(s.items) == 0 {
		return 0, false
	}
	return s.items[len(s.items)-1], true
}

func (s *Stack) Len() int {
	return len(s.items)
}

func (s *Stack) Snapshot() []int {
	items := make([]int, len(s.items))
	copy(items, s.items)
	return items
}
