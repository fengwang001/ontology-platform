package freelist

import "ontology/slot"

const null = -1

// List is an intrusive FIFO list of free slot indexes.
type List[T any] struct {
	next    []int
	head    int
	tail    int
	size    int
	visited int
}

func New[T any](slots []*slot.Slot[T]) *List[T] {
	list := &List[T]{
		next: make([]int, len(slots)),
		head: null,
		tail: null,
	}
	for i := range slots {
		list.PushTail(i)
	}
	return list
}

func (l *List[T]) Len() int { return l.size }

func (l *List[T]) Indexes() []int {
	out := make([]int, 0, l.size)
	for current := l.head; current != null; current = l.next[current] {
		out = append(out, current)
	}
	return out
}

func (l *List[T]) Contains(index int) bool {
	if index < 0 || index >= len(l.next) {
		return false
	}
	for current := l.head; current != null; current = l.next[current] {
		if current == index {
			return true
		}
	}
	return false
}

// Pop removes and returns the head. Only the popped record is visited.
func (l *List[T]) Pop() (int, bool) {
	l.visited = 0
	if l.head == null {
		return 0, false
	}
	index := l.head
	l.visited = 1
	l.head = l.next[index]
	l.next[index] = null
	l.size--
	if l.head == null {
		l.tail = null
	}
	return index, true
}

// PushTail appends a freed index so allocations rotate across slots.
func (l *List[T]) PushTail(index int) bool {
	if index < 0 || index >= len(l.next) {
		return false
	}
	l.next[index] = null
	if l.tail == null {
		l.head = index
	} else {
		l.next[l.tail] = index
	}
	l.tail = index
	l.size++
	return true
}

func (l *List[T]) lastPopVisited() int { return l.visited }
