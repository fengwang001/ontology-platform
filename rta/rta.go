// Package rta 实现带释放抖动与阻塞项的固定优先级响应时间分析（RTA），
// 并支持按 Audsley 算法进行优先级分配与增量任务接纳。
package rta

import (
	"fmt"
	"sync"
)

// MaxTasks 是分析器允许的最大任务数（优先级位宽）。
const MaxTasks = 32

const maxIDBytes = 64

// Task 描述一个周期性任务。
type Task struct {
	ID string // 任务编号，非空且不超过 64 字节
	C  int64  // 最坏执行时间，1 <= C <= 1e9
	T  int64  // 周期（最小到达间隔），1 <= T <= 1e9
	D  int64  // 相对截止期，1 <= D <= T
	J  int64  // 释放抖动，0 <= J <= 1e9
	B  int64  // 阻塞项，0 <= B <= 1e9
}

// Reason 标识拒绝原因，按校验优先级排序。
type Reason int

const (
	ReasonInvalidParam  Reason = iota + 1 // 参数非法
	ReasonDuplicateID                     // 编号重复
	ReasonCapacityFull                    // 容量已满
	ReasonUnschedulable                   // 不可调度
	ReasonNotFound                        // 编号不存在
)

// Error 描述一次被拒绝的操作。
type Error struct {
	Reason     Reason
	Unassigned int // 仅 ReasonUnschedulable：失败时尚未分配的任务个数
}

func (e *Error) Error() string {
	switch e.Reason {
	case ReasonInvalidParam:
		return "rta: invalid parameter"
	case ReasonDuplicateID:
		return "rta: duplicate task id"
	case ReasonCapacityFull:
		return "rta: task capacity full (max 32)"
	case ReasonUnschedulable:
		return fmt.Sprintf("rta: unschedulable (unassigned tasks at failure: %d)", e.Unassigned)
	case ReasonNotFound:
		return "rta: task id not found"
	default:
		return "rta: unknown error"
	}
}

// AddResult 是 Add 的结果。
type AddResult struct {
	Reordered bool // 是否发生整体 Audsley 重排
}

// Scheduler 是并发安全的固定优先级 RTA 分析器。
type Scheduler struct {
	mu         sync.RWMutex
	tasks      map[string]*taskState
	order      []string // 优先级从高到低
	fpSteps    int      // 自上次 resetFPCount 起的不动点求和步数（不含查询）
	fpTerms    int      // applyOrder 重算路径的求和总项数（每步项数=前置任务数）
	checkTerms int      // orderSchedulable 判定路径的求和总项数（不属于重算）
	applyCalls int      // applyOrder 中 analyzeTask 调用次数
}

type taskState struct {
	task Task
	r    int64 // 缓存的响应时间
}

// New 创建容量上限为 32 的分析器。
func New() *Scheduler {
	return &Scheduler{tasks: make(map[string]*taskState)}
}

func validTask(t Task) bool {
	if len(t.ID) == 0 || len(t.ID) > maxIDBytes {
		return false
	}
	if t.C < 1 || t.C > 1_000_000_000 {
		return false
	}
	if t.T < 1 || t.T > 1_000_000_000 {
		return false
	}
	if t.D < 1 || t.D > t.T {
		return false
	}
	if t.J < 0 || t.J > 1_000_000_000 {
		return false
	}
	if t.B < 0 || t.B > 1_000_000_000 {
		return false
	}
	return true
}

func (s *Scheduler) taskList(order []string) []Task {
	out := make([]Task, len(order))
	for i, id := range order {
		out[i] = s.tasks[id].task
	}
	return out
}

// applyOrder 重算 order 中 from 及之后每个任务的响应时间并写缓存。
// 每个任务恰好进行一次 analyzeTask 调用（其不动点各步求和项数等于
// 它之前的任务个数）；调用方须保证该序全员可调度。
func (s *Scheduler) applyOrder(order []string, from int) {
	for i := from; i < len(order); i++ {
		tasks := s.taskList(order[: i+1 : i+1])
		r, steps, ok := analyzeTask(tasks[i], tasks[:i])
		if !ok {
			panic("rta: internal error: applyOrder on unschedulable order")
		}
		s.fpSteps += steps
		s.fpTerms += steps * i // 每个不动点步恰对 i 个前置任务各求一项
		s.applyCalls++
		s.tasks[order[i]].r = r
	}
}

// orderSchedulable 检查给定次序从 from 起是否全员可调度；
// from 之前的任务沿用既有可行序，无需重查。
func (s *Scheduler) orderSchedulable(order []string, from int) bool {
	for i := from; i < len(order); i++ {
		tasks := s.taskList(order[: i+1 : i+1])
		r, steps, ok := analyzeTask(tasks[i], tasks[:i])
		if !ok {
			s.checkTerms += steps * i
			s.fpSteps += steps
			return false
		}
		s.checkTerms += steps * i
		s.fpSteps += steps
		_ = r
	}
	return true
}

// Add 增量接纳一个任务。
//
// 先自最低优先级位置（队尾）起向高位逐个尝试插入，取第一个使全体
// 可调度的位置，已有任务相对次序不变。若所有位置都失败，则对
// "现有任务+新任务"整体重跑 Audsley。被拒绝时任务集与次序不变。
func (s *Scheduler) Add(t Task) (AddResult, error) {
	if !validTask(t) {
		return AddResult{}, &Error{Reason: ReasonInvalidParam}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[t.ID]; exists {
		return AddResult{}, &Error{Reason: ReasonDuplicateID}
	}
	if len(s.order) >= MaxTasks {
		return AddResult{}, &Error{Reason: ReasonCapacityFull}
	}

	// 阶段一：自最低位（排最后）起向前尝试插入。
	for p := len(s.order); p >= 0; p-- {
		candidate := make([]string, 0, len(s.order)+1)
		candidate = append(candidate, s.order[:p]...)
		candidate = append(candidate, t.ID)
		candidate = append(candidate, s.order[p:]...)

		st := &taskState{task: t}
		s.tasks[t.ID] = st
		if s.orderSchedulable(candidate, p) {
			s.order = candidate
			s.applyOrder(s.order, p)
			return AddResult{Reordered: false}, nil
		}
		delete(s.tasks, t.ID)
	}

	// 阶段二：所有插入位置失败，整体重跑 Audsley。
	all := s.taskList(s.order)
	all = append(all, t)
	newOrderTasks, unassigned, ok := audsley(all)
	if !ok {
		// 新任务从未留在 map 中；现有任务集与次序保持不变。
		return AddResult{}, &Error{Reason: ReasonUnschedulable, Unassigned: unassigned}
	}
	s.tasks[t.ID] = &taskState{task: t}
	s.order = make([]string, len(newOrderTasks))
	for i, task := range newOrderTasks {
		s.order[i] = task.ID
	}
	s.applyOrder(s.order, 0)
	return AddResult{Reordered: true}, nil
}

// Remove 删除指定任务，其余次序不变。
func (s *Scheduler) Remove(id string) error {
	if len(id) == 0 {
		return &Error{Reason: ReasonInvalidParam}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.tasks[id]
	if !exists {
		return &Error{Reason: ReasonNotFound}
	}

	pos := 0
	for s.order[pos] != id {
		pos++
	}
	s.order = append(s.order[:pos], s.order[pos+1:]...)
	delete(s.tasks, id)
	// 删除只可能放松干扰；仅需重算排在被删任务之后（现位置 pos 起）的任务。
	s.applyOrder(s.order, pos)
	return nil
}

// Order 返回优先级从高到低的任务编号副本。
func (s *Scheduler) Order() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// Response 返回缓存的响应时间，不执行不动点迭代。
func (s *Scheduler) Response(id string) (int64, error) {
	if len(id) == 0 {
		return 0, &Error{Reason: ReasonInvalidParam}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, exists := s.tasks[id]
	if !exists {
		return 0, &Error{Reason: ReasonNotFound}
	}
	return st.r, nil
}
