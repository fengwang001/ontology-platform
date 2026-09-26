// Package api 对外提供 RR 调度器接口与自检，依赖 rr。
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/proc"
	"ontology/rr"
)

// 对外可判定的哨兵错误，与底层为同一实例，errors.Is 直接可用且互不相同。
var (
	ErrInvalidQuantum = rr.ErrInvalidQuantum
	ErrInvalidProcess = proc.ErrInvalidProcess
	ErrDuplicatePID   = proc.ErrDuplicatePID
)

// Sched 是调度器句柄；Add 可在 Run 前并发调用，SelfCheck 可并发调用。
type Sched struct{ sch *rr.Scheduler }

// New 校验 quantum>=1，失败返回 ErrInvalidQuantum。
func New(quantum int64) (*Sched, error) {
	s, err := rr.New(quantum)
	if err != nil {
		return nil, err
	}
	return &Sched{sch: s}, nil
}

// Add 登记进程；非法参数返回 ErrInvalidProcess，重复 pid 返回 ErrDuplicatePID，
// 被拒时不改变任何已登记进程。
func (s *Sched) Add(pid, arrival, burst int64) error { return s.sch.Add(pid, arrival, burst) }

// Run 模拟调度并返回每个 pid 的完成时刻。
func (s *Sched) Run() (map[int64]int64, error) { return s.sch.Run(), nil }

// naiveSim 朴素参照：逐 tick 让就绪队头减 1，减到 0 完成，
// 否则计满 quantum 个 tick 后移到队尾（排在同时刻新到进程之后）。
func naiveSim(quantum int64, ps [][3]int64) map[int64]int64 {
	type pr struct{ pid, arrival, rem int64 }
	all := make([]pr, 0, len(ps))
	for _, p := range ps {
		all = append(all, pr{p[0], p[1], p[2]})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].arrival != all[j].arrival {
			return all[i].arrival < all[j].arrival
		}
		return all[i].pid < all[j].pid
	})
	done := make(map[int64]int64, len(all))
	var q []int
	next, t, used := 0, int64(0), int64(0)
	for len(done) < len(all) {
		if len(q) == 0 {
			t = all[next].arrival
		}
		for next < len(all) && all[next].arrival <= t {
			q = append(q, next)
			next++
		}
		h := q[0]
		all[h].rem--
		t++
		used++
		if all[h].rem == 0 {
			done[all[h].pid] = t
			q, used = q[1:], 0
		} else if used == quantum {
			for next < len(all) && all[next].arrival <= t {
				q = append(q, next)
				next++
			}
			q, used = append(q[1:], h), 0
		}
	}
	return done
}

// SelfCheck 对内置进程集核验四条不变量，全部通过返回 nil；不触碰接收者状态。
func (s *Sched) SelfCheck() error {
	sets := []struct {
		quantum int64
		ps      [][3]int64
	}{
		{4, [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}}},
		{1, [][3]int64{{1, 0, 3}, {2, 0, 1}, {3, 0, 2}}},
		{3, [][3]int64{{5, 2, 7}, {2, 0, 1}, {9, 9, 4}, {4, 2, 3}}},
		{7, [][3]int64{{1, 5, 1}}},
	}
	for _, c := range sets {
		sch, err := New(c.quantum)
		if err != nil {
			return err
		}
		var sumBurst, maxCT int64
		for _, p := range c.ps {
			if err := sch.Add(p[0], p[1], p[2]); err != nil {
				return err
			}
			sumBurst += p[2]
		}
		got, _ := sch.Run()
		want := naiveSim(c.quantum, c.ps) // 不变量 1：与朴素参照一致
		seen := make(map[int64]bool, len(c.ps))
		for _, p := range c.ps {
			ct := got[p[0]]
			if ct != want[p[0]] {
				return fmt.Errorf("selfcheck: pid=%d completion=%d want=%d", p[0], ct, want[p[0]])
			}
			if ct < p[1]+p[2] || seen[ct] { // 不变量 3：不早于 arrival+burst 且互不相同
				return fmt.Errorf("selfcheck: pid=%d bad completion=%d", p[0], ct)
			}
			seen[ct] = true
			maxCT = max(maxCT, ct)
		}
		if sumArrivalZero(c.ps) && maxCT != sumBurst { // 不变量 2：守恒，CPU 不空转不少跑
			return fmt.Errorf("selfcheck: makespan=%d sum(burst)=%d", maxCT, sumBurst)
		}
	}
	// 不变量 4：三类拒绝互不相同且不留痕，之后仍可正常使用。
	bad, _ := New(3)
	if err := bad.Add(1, 0, 2); err != nil {
		return err
	}
	e1, e2 := bad.Add(2, -1, 1), bad.Add(1, 0, 1)
	_, e3 := New(0)
	if !errors.Is(e1, ErrInvalidProcess) || !errors.Is(e2, ErrDuplicatePID) || !errors.Is(e3, ErrInvalidQuantum) {
		return errors.New("selfcheck: rejection not distinguishable")
	}
	if got, _ := bad.Run(); len(got) != 1 || got[1] != 2 {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}

func sumArrivalZero(ps [][3]int64) bool {
	for _, p := range ps {
		if p[1] != 0 {
			return false
		}
	}
	return true
}
