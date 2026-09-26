// Package rr 实现时间片轮转（RR）CPU 调度模拟，按事件整块推进；依赖方向 rr -> proc。
package rr

import (
	"errors"
	"fmt"
	"sort"

	"ontology/proc"
)

// ErrBadQuantum：时间片长度必须至少为 1。
var ErrBadQuantum = errors.New("rr: quantum must be >= 1")

// Scheduler 持有已登记进程与时间片长度。
type Scheduler struct {
	quantum   int64
	set       *proc.Set
	tickSteps int64 // 本 Run 中逐 1 时间单位推进的次数；整块推进恒 0，非导出、不进任何公开接口
}

// New：quantum < 1 整体失败（此刻尚无进程，不存在“污染已登记状态”）。
func New(quantum int64) (*Scheduler, error) {
	if quantum < 1 {
		return nil, ErrBadQuantum
	}
	return &Scheduler{quantum: quantum, set: proc.NewSet()}, nil
}

// Add：先 proc.New 字段校验，通过后才 set.Add 加锁查重并插入；拒绝都在插入前，失败不留痕。
func (s *Scheduler) Add(pid, arrival, burst int64) error {
	p, err := proc.New(pid, arrival, burst)
	if err != nil {
		return err
	}
	return s.set.Add(p)
}

// Run 执行一次模拟，返回 pid -> 完成时刻；重复调用结果一致。
func (s *Scheduler) Run() map[int64]int64 { c, _ := s.simulate(); return c }

// ordered：就绪序 (arrival, pid) 升序。
func ordered(in []proc.Proc) []proc.Proc {
	out := append([]proc.Proc(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Arrival < out[j].Arrival || out[i].Arrival == out[j].Arrival && out[i].PID < out[j].PID
	})
	return out
}

// simulate 事件驱动整块推进；另返回每进程被服务总时长，供守恒核验。
func (s *Scheduler) simulate() (map[int64]int64, map[int64]int64) {
	s.tickSteps = 0
	in := ordered(s.set.List())
	rem, sv, done := map[int64]int64{}, map[int64]int64{}, map[int64]int64{}
	for _, p := range in {
		rem[p.PID] = p.Burst
	}
	var q []int64
	idx, t := 0, int64(0)
	for idx < len(in) || len(q) > 0 {
		if len(q) == 0 {
			t = in[idx].Arrival // 就绪空：跳到最近的下一个到达时刻
		}
		for idx < len(in) && in[idx].Arrival <= t { // 片起点（含）前已到达者入队
			q, idx = append(q, in[idx].PID), idx+1
		}
		cur := q[0]
		q = q[1:]
		run := s.quantum // 整块推进 min(remaining, quantum)，不逐 tick
		if rem[cur] < run {
			run = rem[cur]
		}
		end := t + run
		t, rem[cur], sv[cur] = end, rem[cur]-run, sv[cur]+run
		for idx < len(in) && in[idx].Arrival <= end { // (片起点,片终点] 到达者先入队尾
			q, idx = append(q, in[idx].PID), idx+1
		}
		if rem[cur] == 0 {
			done[cur] = end // 完成=片终点；每片至多一个完成，故完成时刻互不相同
		} else {
			q = append(q, cur) // 未完成入队尾，排在片期间新到达者之后
		}
	}
	return done, sv
}

// SelfTest 对内置四进程在 5 档 quantum 下核验不变量 1-3。
// 期望值即逐 tick 朴素参照的结果（quantum=4 的推导见 NOTES.md 时间片表）。
// 不变量 4（失败不留痕）由 api 层在已登记集合上核验。
func SelfTest() error {
	spec := [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}}
	am := map[int64]int64{1: 0, 2: 1, 3: 3, 4: 3}
	bm := map[int64]int64{1: 10, 2: 4, 3: 3, 4: 2}
	want := map[int64]map[int64]int64{
		1: {1: 19, 2: 12, 3: 13, 4: 10},
		2: {1: 19, 2: 12, 3: 15, 4: 10},
		3: {1: 19, 2: 15, 3: 9, 4: 11},
		4: {1: 19, 2: 8, 3: 11, 4: 13},
		7: {1: 19, 2: 11, 3: 14, 4: 16},
	}
	for _, qn := range []int64{1, 2, 3, 4, 7} {
		s, _ := New(qn)
		for _, x := range spec {
			if err := s.Add(x[0], x[1], x[2]); err != nil {
				return err
			}
		}
		got, sv := s.simulate()
		seen := map[int64]bool{}
		for pid, c := range got {
			switch {
			case c != want[qn][pid]: // 不变量 1：与朴素参照一致
				return fmt.Errorf("self-test q=%d pid=%d: %d!=naive %d", qn, pid, c, want[qn][pid])
			case sv[pid] != bm[pid]: // 不变量 2：服务守恒
				return fmt.Errorf("self-test q=%d pid=%d: served %d!=burst %d", qn, pid, sv[pid], bm[pid])
			case c < am[pid]+bm[pid] || seen[c]: // 不变量 3：下界且完成时刻互不相同
				return fmt.Errorf("self-test q=%d pid=%d: bad completion %d", qn, pid, c)
			}
			seen[c] = true
		}
	}
	return nil
}

// SelfTestCounter 核验整块推进：burst=m、quantum>=m 时 tickSteps 恒 0；只返回成败，不暴露数值。
func SelfTestCounter() error {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		s, err := New(m)
		if err != nil {
			return err
		}
		if err := s.Add(1, 0, m); err != nil {
			return err
		}
		if c := s.Run(); c[1] != m || s.tickSteps != 0 { // 白盒核验；外部只能拿到成败
			return fmt.Errorf("self-test counter failed at m=%d", m)
		}
	}
	return nil
}
