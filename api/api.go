// Package api 对外提供重复定时任务调度器。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/fire"
	"ontology/period"
)

// 可判定哨兵错误，互不相同。
var (
	ErrEmptyID     = errors.New("api: empty id")
	ErrDuplicateID = errors.New("api: duplicate id")
	ErrUnknownMode = errors.New("api: unknown mode")
	ErrBadInterval = errors.New("api: interval <= 0")
	ErrBadDuration = errors.New("api: duration < 0")
	ErrNoSuchTask  = errors.New("api: no such task")
)

// Mode 调度模式，仅 Rate 与 Delay 两种。
type Mode = fire.Mode

const (
	Rate  = fire.Rate
	Delay = fire.Delay
)

// Scheduler 并发安全的内存调度器。
type Scheduler struct {
	mu    sync.Mutex
	tasks map[string]*period.Task
}

// New 返回空调度器。
func New() *Scheduler {
	return &Scheduler{tasks: make(map[string]*period.Task)}
}

// AddTask 注册任务。任何校验失败都不改变状态。
func (s *Scheduler) AddTask(id string, mode Mode, interval int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if mode != Rate && mode != Delay {
		return ErrUnknownMode
	}
	if interval <= 0 {
		return ErrBadInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicateID
	}
	s.tasks[id] = period.New(mode, interval)
	return nil
}

// Run 执行 id 任务一次，返回 (开始, 结束)。失败不改变状态。
func (s *Scheduler) Run(id string, duration int64) (int64, int64, error) {
	if duration < 0 {
		return 0, 0, ErrBadDuration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return 0, 0, ErrNoSuchTask
	}
	start, end := t.Run(duration)
	return start, end, nil
}

// naive 朴素参照：rate 逐个网格点步进，delay 按结束+interval。
func naive(mode Mode, interval int64, durs []int64) [][2]int64 {
	out := make([][2]int64, 0, len(durs))
	var lastEnd int64
	for i, d := range durs {
		var start int64
		if mode == Rate {
			for start = 0; start < lastEnd; start += interval {
			}
		} else if i == 0 {
			start = 0
		} else {
			start = lastEnd + interval
		}
		out = append(out, [2]int64{start, start + d})
		lastEnd = start + d
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *Scheduler) SelfCheck() error {
	ref := New()
	durs := []int64{3, 15, 2, 4, 0, 25, 7}
	for _, mode := range []Mode{Rate, Delay} {
		id := fmt.Sprintf("self-%d", mode)
		if err := ref.AddTask(id, mode, 10); err != nil {
			return err
		}
		want := naive(mode, 10, durs)
		var prevEnd int64 = -1
		for i, d := range durs {
			start, end, err := ref.Run(id, d)
			if err != nil {
				return err
			}
			if start != want[i][0] || end != want[i][1] { // 不变量 1
				return fmt.Errorf("api: selfcheck naive mismatch at %d", i)
			}
			if mode == Rate && start%10 != 0 { // 不变量 2
				return fmt.Errorf("api: selfcheck rate drift at %d", i)
			}
			if mode == Delay && i > 0 && start != prevEnd+10 { // 不变量 3
				return fmt.Errorf("api: selfcheck delay drift at %d", i)
			}
			prevEnd = end
		}
	}
	// 不变量 4：五类拒绝均不改变状态，且之后仍可正常使用。
	before, _, err := ref.Run("self-0", 0)
	if err != nil {
		return err
	}
	for _, e := range []error{
		ref.AddTask("", Rate, 1), ref.AddTask("self-0", Rate, 1),
		ref.AddTask("x", Mode(99), 1), ref.AddTask("y", Rate, 0),
		func() error { _, _, e := ref.Run("self-0", -1); return e }(),
	} {
		if e == nil {
			return errors.New("api: selfcheck rejection missing")
		}
	}
	after, _, err := ref.Run("self-0", 0)
	if err != nil || after != before { // duration=0 的 rate 下次开始仍在同一网格点
		return errors.New("api: selfcheck state changed by rejection")
	}
	return nil
}
