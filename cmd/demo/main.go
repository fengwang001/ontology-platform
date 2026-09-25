package main

import (
	"errors"
	"fmt"

	"ontology/ring"
	"ontology/seq"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
		return
	}
	failed++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	b, err := ring.New[int](4)
	check("New(4) succeeds", err == nil)

	full := true
	for i := 0; i < 4; i++ {
		full = full && b.Enqueue(i) == nil
	}
	check("4 enqueues succeed", full && b.Full())
	check("5th enqueue is ErrFull", errors.Is(b.Enqueue(9), seq.ErrFull))

	_, badCap := ring.New[int](0)
	check("New(0) is ErrBadCap", errors.Is(badCap, seq.ErrBadCap))

	fifo := true
	for want := 0; want < 4; want++ {
		v, ok := b.Dequeue()
		fifo = fifo && ok && v == want
	}
	check("FIFO order preserved", fifo)

	v, ok := b.Dequeue()
	check("empty dequeue is (0,false)", v == 0 && !ok && b.Empty() && !b.Full())

	p, c, _ := seq.NewPair[int](2)
	check("seq pair passes value", p.Enqueue(7) == nil)
	got, _ := c.Dequeue()
	check("pair consumer sees 7", got == 7)

	fmt.Printf("OK   total: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		panic("demo checks failed")
	}
}
