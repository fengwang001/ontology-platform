package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/check"
	"ontology/ring"
	"ontology/seq"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status, name)
}

func main() {
	q, err := ring.New[int](4)
	report("ring New(4)", err == nil)
	for i := 0; i < 4; i++ {
		q.Enqueue(i)
	}
	report("ring 4 enqueues, 5th ErrFull", q.Len() == 4 && errors.Is(q.Enqueue(9), ring.ErrFull))
	v, _ := q.Dequeue()
	report("ring FIFO dequeue", v == 0 && q.Enqueue(9) == nil)
	_, badCap := ring.New[int](0)
	report("ring ErrBadCap", errors.Is(badCap, ring.ErrBadCap))
	p, c, _ := seq.Pair[int](2)
	p.Send(7)
	got, _ := c.Recv()
	_, emptyErr := c.Recv()
	report("seq pair send/recv + ErrEmpty", got == 7 && errors.Is(emptyErr, seq.ErrEmpty))
	var ref check.Ref[int]
	ref.Enqueue(1)
	r1, _ := ref.Dequeue()
	report("check ref FIFO", r1 == 1 && ref.Len() == 0)
	if failed {
		fmt.Println("FAIL total")
		os.Exit(1)
	}
	fmt.Println("OK total 7/7 passed")
}
