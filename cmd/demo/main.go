package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/fanout"
)

type row struct {
	q0, q1 []int64
	d0, d1 int
}

func verdict(name string, ok bool) bool {
	if ok {
		fmt.Printf("%s OK\n", name)
	} else {
		fmt.Printf("%s FAIL\n", name)
	}
	return ok
}

// sixReplay runs NOTES.md steps 1..k on a fresh bus and returns drained
// queue contents plus both drop counts.
func sixReplay(k int) (q0, q1 []int64, d0, d1 int) {
	a, _ := api.New(2, 2)
	for i := 1; i <= k; i++ {
		switch i {
		case 1, 2, 3:
			a.Publish(int64(i))
		case 4:
			a.Consume(0)
		case 5:
			a.Publish(4)
		case 6:
			a.Consume(1)
		}
	}
	drain := func(si int) []int64 {
		var out []int64
		for ev, ok := a.Consume(si); ok; ev, ok = a.Consume(si) {
			out = append(out, ev)
		}
		return out
	}
	return drain(0), drain(1), a.DropCount(0), a.DropCount(1)
}

func eq(x, y []int64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func main() {
	want := []row{
		{[]int64{1}, []int64{1}, 0, 0},
		{[]int64{1, 2}, []int64{1, 2}, 0, 0},
		{[]int64{1, 2}, []int64{1, 2}, 1, 1},
		{[]int64{2}, []int64{1, 2}, 1, 1},
		{[]int64{2, 4}, []int64{1, 2}, 1, 2},
		{[]int64{2, 4}, []int64{2}, 1, 2},
	}
	sixOK := true
	for k, w := range want {
		q0, q1, d0, d1 := sixReplay(k + 1)
		sixOK = sixOK && eq(q0, w.q0) && eq(q1, w.q1) && d0 == w.d0 && d1 == w.d1
	}
	if !verdict("six-step table", sixOK) {
		return
	}

	a, _ := api.New(2, 2)
	a.Publish(1)
	a.Publish(2)
	a.Publish(3)
	td := a.DropCount(0) == 1 // tail-drop keeps [1,2], drops newest 3
	v, _ := a.Consume(0)
	fifo := v == 1 // FIFO: oldest out first
	verdict("tail-drop", td)
	verdict("fifo-order", fifo)

	a.Consume(0) // free a slot only on s0
	a.Publish(4) // s1 full drops 4; s0 must still get 4
	x0, _ := a.Consume(0)
	d1 := a.DropCount(1)
	verdict("slow-consumer-isolation", x0 == 4 && d1 == 2)

	verdict("naive-reference", a2().SelfCheck() == nil)

	_, eN := api.New(0, 2)
	_, eC := api.New(2, 0)
	rec := guard(func() { a.Consume(9) })
	distinct := errors.Is(eN, api.ErrInvalidN) && errors.Is(eC, api.ErrInvalidC) &&
		!errors.Is(eN, eC) && errors.Is(rec.(error), api.ErrInvalidSubscriber)
	verdict("three-distinct-errors", distinct)

	a.Publish(8)
	_ = guard(func() { a.Consume(-1) })
	keep, ok := a.Consume(1) // s1 still holds [1,2]; rejected call left no trace
	verdict("rejected-no-trace", keep == 1 && ok)

	verdict("slots-constant-large-m", fanout.VerifyFullnessCost() == nil)
	verdict("concurrent-consume", concurrentOK())
}

func a2() *api.API { a, _ := api.New(2, 2); return a }
func guard(f func()) (r any) {
	defer func() { r = recover() }()
	f()
	return
}

// concurrentOK runs one goroutine per subscriber; each drains a queue
// prefilled with distinct values and no sleep is used.
func concurrentOK() bool {
	const n, c = 4, 8
	a, _ := api.New(n, c)
	for v := 0; v < c; v++ {
		a.Publish(int64(v + 1))
	}
	got := make([][]int64, n)
	var wg sync.WaitGroup
	for si := 0; si < n; si++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			for ev, ok := a.Consume(si); ok; ev, ok = a.Consume(si) {
				got[si] = append(got[si], ev)
			}
		}(si)
	}
	wg.Wait()
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	for si := 0; si < n; si++ {
		if !eq(got[si], want) || a.QueueLen(si) != 0 {
			return false
		}
	}
	return true
}
