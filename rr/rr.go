// Package rr 实现事件驱动的时间片轮转调度模拟，依赖 proc。
package rr

import (
	"errors"
	"sync"

	"ontology/proc"
)

// ErrInvalidQuantum 表示时间片长度非法（quantum <= 0）。
var ErrInvalidQuantum = errors.New("rr: quantum must be >= 1")

// Scheduler 按 RR(quantum) 模拟调度。Add 可在 Run 前并发调用。
type Scheduler struct {
	mu      sync.Mutex
	quantum int64
	reg     *proc.Registry
	ticks   int64 // 本次 Run 中逐 1 个时间单位推进的次数；整块推进时恒为 0
}

// New 校验 quantum>=1，失败时不产生调度器。
func New(quantum int64) (*Scheduler, error) {
	if quantum < 1 {
		return nil, ErrInvalidQuantum
	}
	return &Scheduler{quantum: quantum, reg: proc.NewRegistry()}, nil
}

// Add 登记一个进程；非法参数或重复 pid 被拒且不改变已有登记。
func (s *Scheduler) Add(pid, arrival, burst int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reg.Add(pid, arrival, burst)
}

// Run 模拟调度，返回每个 pid 的完成时刻。可重复调用，结果相同。
func (s *Scheduler) Run() map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ticks = 0
	return s.simulate(s.reg.Snapshot())
}

// simulate 事件驱动：每片按 min(remaining, quantum) 整块推进。
// 不变量：弹出队头前，所有 arrival <= 当前时间的进程均已入队。
func (s *Scheduler) simulate(sorted []proc.Proc) map[int64]int64 {
	done := make(map[int64]int64, len(sorted))
	var queue []int // 存 sorted 的下标，队头在前
	next, t := 0, int64(0)
	for len(done) < len(sorted) {
		if len(queue) == 0 { // 队列空：时间跳到最近到达
			t = sorted[next].Arrival
		}
		for next < len(sorted) && sorted[next].Arrival <= t {
			queue = append(queue, next)
			next++
		}
		head := queue[0]
		queue = queue[1:]
		run := min(sorted[head].Remaining, s.quantum) // 整块推进，不逐 tick
		sorted[head].Remaining -= run
		t += run
		for next < len(sorted) && sorted[next].Arrival <= t { // 片期间到达者先入队
			queue = append(queue, next)
			next++
		}
		if sorted[head].Remaining == 0 {
			done[sorted[head].PID] = t
		} else {
			queue = append(queue, head) // 被抢占者排在新到进程之后
		}
	}
	return done
}
