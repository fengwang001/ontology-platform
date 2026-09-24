// Command demo 对 flowq/drr 做冒烟判定：每行 OK/FAIL，最后一行总计。
package main

import (
	"fmt"
	"os"

	"ontology/drr"
	"ontology/flowq"
)

var failed bool

func check(ok bool, msg string) {
	if ok {
		fmt.Println("OK", msg)
		return
	}
	fmt.Println("FAIL", msg)
	failed = true
}

func main() {
	var q flowq.Queue
	q.Push(100)
	front, _ := q.Front()
	popped, _ := q.Pop()
	check(front == 100 && popped == 100 && q.Len() == 0, "flowq fifo push/pop")

	s := drr.New()
	s.AddFlow(1, 500)
	s.AddFlow(2, 500)
	s.Enqueue(1, 300)
	id, size, ok := s.Dequeue()
	check(ok && id == 1 && size == 300, "drr deficit dequeue")

	check(s.Enqueue(9, 10) == drr.ErrUnknownFlow, "unknown flow rejected")
	check(s.Enqueue(1, 0) == drr.ErrBadSize, "bad size rejected")

	for range 1000 { // X 已发空离场，Y 独占 1000 轮
		s.Enqueue(2, 500)
		s.Dequeue()
	}
	for range 10 {
		s.Enqueue(1, 500)
	}
	_, burst, _ := s.Dequeue()
	check(burst <= 500+drr.MaxBlock-1, "idle return burst bounded")

	f := drr.New()
	f.AddFlow(1, 2048)
	f.AddFlow(2, 2048)
	fair := true
	for range 100 {
		f.Enqueue(1, drr.MaxBlock)
		f.Enqueue(2, drr.MaxBlock)
		f.Dequeue()
		st := f.Stats()
		if d := st.Flows[1].Sent - st.Flows[2].Sent; d < -10238 || d > 10238 {
			fair = false
		}
	}
	check(fair, "byte fairness bound holds")

	conserve := true
	for _, fs := range f.Stats().Flows {
		conserve = conserve && fs.Sent+fs.Queued == fs.Enqueued
	}
	check(conserve, "sent+queued == enqueued")

	if failed {
		fmt.Println("FAIL total")
		os.Exit(1)
	}
	fmt.Println("OK total")
}
