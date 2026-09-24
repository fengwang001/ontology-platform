// Command demo exercises the mlfq scheduler and prints OK/FAIL checks.
package main

import (
	"errors"
	"fmt"
	"ontology/mlfq"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	s := mlfq.New(mlfq.Config{Levels: 3, Boost: 20, MaxJobs: 100, Quotas: []int{4, 8, 16}})
	check("submit", s.Submit(1, 10) == nil && s.Submit(2, 3) == nil)
	check("duplicate", errors.Is(s.Submit(1, 1), mlfq.ErrDuplicate))
	check("unknown-yield", errors.Is(s.Yield(99), mlfq.ErrUnknown))
	id, _ := s.Step()
	check("fifo-top", id == 1)
	for i := 0; i < 3; i++ {
		s.Step()
	}
	id, _ = s.Step()
	check("demote-after-quota", id == 2) // job1 用满 Q[0]=4 降级，轮到 job2
	ran := map[int]int{}
	for s.Stats().Active > 0 {
		id, _ := s.Step()
		ran[id]++
	}
	check("conserve", ran[1] == 6 && ran[2] == 2) // 剩余刻数恰好跑完
	s2 := mlfq.New(mlfq.Config{Levels: 2, Boost: 3, MaxJobs: 10, Quotas: []int{2, 4}})
	s2.Submit(1, 9)
	for i := 0; i < 4; i++ { // 2 刻降级到 L1，第 3 刻提升回 L0，第 4 刻只查 1 个队列
		s2.Step()
	}
	check("boost-returns-top", s2.Stats().LastCheck == 1)
	check("scan-bound", s.Stats().LastCheck <= 4)
	if fails == 0 {
		fmt.Println("OK all 8 checks passed")
	} else {
		fmt.Println("FAIL", fails, "checks failed")
	}
}
