// Package sched 实现抢占式调度状态机：current/suspend/done。
package sched

import (
	"errors"
	"sync"

	"ontology/q"
)

// 哨兵错误。
var (
	ErrInvalidPrio = errors.New("sched: negative priority")
	ErrDuplicateID = errors.New("sched: duplicate id")
	ErrIdle        = errors.New("sched: no event to process")
)

// Cursor 一个批次的现场：优先级与下一个待处理事件的下标。
type Cursor struct {
	Prio int
	Pos  int
}

// Scheduler 调度状态机。
type Scheduler struct {
	mu      sync.Mutex
	pending *q.Queues
	current *Cursor
	suspend []Cursor
	done    []int
	seen    map[int]bool
	probes  int // 最近一次 Process 为挑选批次检查的分档个数（非导出）
}

// New 创建调度器。
func New() *Scheduler { return &Scheduler{} }

// Submit 提交事件；更高优先级到达时抢占当前批次并保存现场。
func (s *Scheduler) Submit(id, prio int) error { return nil }

// Process 推进一个事件。
func (s *Scheduler) Process() (int, int, error) { return 0, 0, ErrIdle }

// Done 返回已处理事件 ID 顺序的副本。
func (s *Scheduler) Done() []int { return nil }

// State 返回当前状态快照（pend 为 prio→队列 的副本，suspend 自底向上）。
func (s *Scheduler) State() (pend map[int][]int, cur *Cursor, suspend []Cursor, done []int) {
	return nil, nil, nil, nil
}
