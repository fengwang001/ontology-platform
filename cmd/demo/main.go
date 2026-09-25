// 防博弈 MLFQ 调度器演示：8 条判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/mlfq"
)

func main() {
	pass := 0
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	s := mlfq.New(mlfq.Config{Levels: 2, Quotas: []int{4, 8}, Boost: 100, MaxJobs: 2})
	check("submit", s.Submit(1, 6) == nil && s.Submit(2, 6) == nil)
	check("duplicate", errors.Is(s.Submit(1, 1), mlfq.ErrDuplicate))
	check("full", errors.Is(s.Submit(3, 1), mlfq.ErrFull))
	check("unknown-yield", errors.Is(s.Yield(9), mlfq.ErrUnknown))
	id, _ := s.Step()
	check("fifo-highest", id == 1)
	g := mlfq.New(mlfq.Config{Levels: 2, Quotas: []int{4, 99}, Boost: 1000, MaxJobs: 2})
	g.Submit(1, 100)
	g.Submit(2, 100)
	runs, last := 0, 0
	for i := 0; i < 9; i++ {
		last, _ = g.Step()
		if last == 1 {
			runs++
			if runs%3 == 0 {
				g.Yield(1)
			}
		}
	}
	check("gamer-demoted", last == 2)
	c := mlfq.New(mlfq.Config{Levels: 3, Quotas: []int{2, 3, 5}, Boost: 7, MaxJobs: 4})
	c.Submit(1, 3)
	c.Submit(2, 5)
	ran := map[int]int{}
	for c.Stats().Active > 0 {
		id, _ := c.Step()
		ran[id]++
	}
	check("conservation", ran[1] == 3 && ran[2] == 5 && c.Stats().Ticks == 8)
	b := mlfq.New(mlfq.Config{Levels: 2, Quotas: []int{2, 99}, Boost: 4, MaxJobs: 2})
	b.Submit(1, 20)
	b.Submit(2, 20)
	for i := 0; i < 6; i++ {
		b.Step()
	}
	id, _ = b.Step()
	check("boost", id == 2)
	fmt.Printf("OK total %d/8\n", pass)
	if pass < 8 {
		os.Exit(1)
	}
}
