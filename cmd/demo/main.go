package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/bus"
	"ontology/fanout"
)

func report(l string, c bool) {
	if c {
		fmt.Println("OK " + l)
	}
	if !c {
		fmt.Println("FAIL " + l)
	}
}
func drain(b *bus.Bus, si int) (out []int64) {
	for {
		v, ok := b.Consume(si)
		if !ok {
			return out
		}
		out = append(out, v)
	}
}
func recErr(b *bus.Bus, si int) (err error) {
	defer func() {
		if e, ok := recover().(error); ok {
			err = e
		}
	}()
	b.Consume(si)
	return
}

func main() {
	wantQ := [][2][]int64{{{1}, {1}}, {{1, 2}, {1, 2}}, {{1, 2}, {1, 2}}, {{2}, {1, 2}}, {{2, 4}, {1, 2}}, {{2, 4}, {2}}}
	wantD := [][2]int{{0, 0}, {0, 0}, {1, 1}, {1, 1}, {1, 2}, {1, 2}}
	ops := []func(*bus.Bus){
		func(b *bus.Bus) { b.Publish(1) }, func(b *bus.Bus) { b.Publish(2) }, func(b *bus.Bus) { b.Publish(3) },
		func(b *bus.Bus) { b.Consume(0) }, func(b *bus.Bus) { b.Publish(4) }, func(b *bus.Bus) { b.Consume(1) },
	}
	six := true
	for k := 1; k <= 6; k++ {
		b, _ := bus.New(2, 2)
		for j := 0; j < k; j++ {
			ops[j](b)
		}
		wq, wd := wantQ[k-1], wantD[k-1]
		six = six && reflect.DeepEqual(drain(b, 0), wq[0]) &&
			reflect.DeepEqual(drain(b, 1), wq[1]) &&
			b.DropCount(0) == wd[0] && b.DropCount(1) == wd[1]
	}
	report("six-step trace N=2,C=2", six)
	b, _ := bus.New(1, 2)
	b.Publish(1)
	b.Publish(2)
	b.Publish(3)
	v, ok := b.Consume(0)
	report("tail-drop", ok && v == 1 && b.DropCount(0) == 1)
	b, _ = bus.New(2, 1)
	b.Publish(1)
	b.Publish(2)
	b.Consume(0)
	b.Publish(3)
	v, _ = b.Consume(0)
	report("slow-consumer isolation", v == 3 && b.DropCount(0) == 1 && b.DropCount(1) == 2)
	b, _ = bus.New(1, 5)
	for _, e := range []int64{10, 20, 30} {
		b.Publish(e)
	}
	report("FIFO order", reflect.DeepEqual(drain(b, 0), []int64{10, 20, 30}))
	b, _ = bus.New(3, 3)
	rq := make([][]int64, 3)
	rd := make([]int, 3)
	pub := func(e int64) {
		b.Publish(e)
		for i := range rq {
			if len(rq[i]) == 3 {
				rd[i]++
			} else {
				rq[i] = append(rq[i], e)
			}
		}
	}
	pop := func(si int) {
		if _, k := b.Consume(si); k && len(rq[si]) > 0 {
			rq[si] = rq[si][1:]
		}
	}
	pub(1)
	pub(2)
	pop(0)
	pub(3)
	pop(1)
	pub(4)
	pop(0)
	ref := true
	for i := range rq {
		ref = ref && reflect.DeepEqual(drain(b, i), rq[i]) && b.DropCount(i) == rd[i]
	}
	report("naive reference equivalence", ref)
	_, eN := bus.New(0, 1)
	_, eC := bus.New(1, 0)
	b, _ = bus.New(1, 1)
	eI := recErr(b, 5)
	report("three distinct sentinel errors",
		errors.Is(eN, bus.ErrInvalidN) && errors.Is(eC, bus.ErrInvalidC) &&
			errors.Is(eI, bus.ErrSubscriberIndex) && eN != eC && eN != eI && eC != eI)
	b, _ = bus.New(1, 1)
	b.Publish(9)
	_ = recErr(b, -1)
	v, ok = b.Consume(0)
	report("rejected op leaves no trace", ok && v == 9 && b.DropCount(0) == 0)
	report("slot-check O(1) across m=100..10000", fanout.OfferCheckCostIsConstant())
	eb, eNew := api.New(2, 2)
	_, eBad := api.New(-1, 1)
	report("api facade + SelfCheck", eNew == nil && errors.Is(eBad, api.ErrInvalidN) && eb.QueueLen(0) == 0 && eb.SelfCheck())
	const n = 8
	b, _ = bus.New(n, 6)
	for _, e := range []int64{10, 11, 12, 13, 14, 15} {
		b.Publish(e)
	}
	res := make([][]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			for {
				x, k := b.Consume(si)
				if !k {
					return
				}
				res[si] = append(res[si], x)
			}
		}(i)
	}
	wg.Wait()
	cc, wantC := true, []int64{10, 11, 12, 13, 14, 15}
	for _, r := range res {
		cc = cc && reflect.DeepEqual(r, wantC)
	}
	report("concurrent consume distinct subscribers", cc)
}
