package main

import (
	"errors"
	"fmt"

	"ontology/queue"
	"ontology/stack"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", verdict(ok), name)
}

func verdict(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func main() {
	var s stack.Stack[int]
	s.Push(1)
	s.Push(2)
	top, _ := s.Pop()
	check("stack LIFO", top == 2 && s.Len() == 1)

	_, popErr := s.Pop()
	_, popErr2 := s.Pop()
	_, peekErr := s.Peek()
	check("stack sentinel errors", popErr == nil &&
		errors.Is(popErr2, stack.ErrPopEmpty) &&
		errors.Is(peekErr, stack.ErrPeekEmpty) &&
		!errors.Is(popErr2, stack.ErrPeekEmpty))

	var q queue.Queue[int]
	for i := 1; i <= 5; i++ {
		q.Enqueue(i)
	}
	fifo := q.Len() == 5
	for i := 1; i <= 5; i++ {
		v, ok := q.Dequeue()
		fifo = fifo && ok && v == i
	}
	check("queue FIFO 1..5", fifo)

	_, deqOK := q.Dequeue()
	_, peekOK := q.Peek()
	check("queue empty semantics", !deqOK && !peekOK && q.Len() == 0)

	const ops = 10000
	var q2 queue.Queue[int]
	for i := 0; i < ops; i++ {
		if i%2 == 0 {
			q2.Enqueue(i)
		} else {
			q2.Dequeue()
		}
	}
	check("amortized moves <= 2/op", q2.Moves() <= 2*ops)

	fmt.Printf("%s summary\n", verdict(!failed))
}
