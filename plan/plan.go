// Package plan 在产线序列之上提供插单、冻结区推进、移除与交期承诺。
package plan

import (
	"errors"
	"sync"

	"ontology/line"
	"ontology/matrix"
)

var (
	// ErrInvalidArgument 参数非法。
	ErrInvalidArgument = errors.New("plan: invalid argument")
	// ErrClockRollback now 小于已接受操作的最大 now。
	ErrClockRollback = errors.New("plan: clock rollback")
	// ErrDuplicate 工单号重复。
	ErrDuplicate = errors.New("plan: duplicate work order")
	// ErrNotFound 工单号不存在。
	ErrNotFound = errors.New("plan: work order not found")
	// ErrFrozen 工单已冻结。
	ErrFrozen = errors.New("plan: work order frozen")
	// ErrDueConflict 严格插单交期冲突。
	ErrDueConflict = errors.New("plan: due date conflict")
	// ErrInvalidState 计划非空时修改换型矩阵。
	ErrInvalidState = errors.New("plan: invalid state")
)

// WorkOrder 是插单请求的输入。
type WorkOrder struct {
	ID       string
	Family   string
	Duration int64
	Due      int64
	Ready    int64
}

// Result 是工单的时刻与状态视图。
type Result struct {
	ID     string
	Start  int64
	End    int64
	Late   bool
	Frozen bool
}

// Scheduler 是并发安全的工单排产器。
type Scheduler struct {
	mu     sync.RWMutex
	f      int64
	ln     *line.Line
	mat    *matrix.Matrix
	nowMax int64
}

// New 创建排产器；F 为冻结提前量，T0 为产线可用时刻，f0 为初始产品族。
func New(F, T0 int64, f0 string) (*Scheduler, error) {
	if F < 0 || F > 1_000_000 || len(f0) < 1 || len(f0) > 32 {
		return nil, ErrInvalidArgument
	}
	s := &Scheduler{f: F}
	s.ln = line.New(T0, f0, func(a, b string) int64 { return s.mat.Get(a, b) })
	s.mat = matrix.New(currentLine{s})
	return s, nil
}

// currentLine 让 matrix 的空序列检查始终指向排产器当前持有的序列
// （Insert 接受后序列会被试算克隆替换）。
type currentLine struct{ s *Scheduler }

func (g currentLine) Empty() bool { return g.s.ln.Empty() }

// SetChangeover 设定产品族 a→b 的换型分钟数，仅允许在计划序列为空时调用。
func (s *Scheduler) SetChangeover(a, b string, minutes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mat.SetChangeover(a, b, minutes); err != nil {
		switch {
		case errors.Is(err, matrix.ErrInvalidArgument):
			return ErrInvalidArgument
		case errors.Is(err, matrix.ErrInvalidState):
			return ErrInvalidState
		}
		return err
	}
	return nil
}

// Insert 接受或拒绝一张工单；strict 为真时做新增交期冲突判定。
// 返回新单的 start、end 与是否延期。被拒绝时全部状态保持不变。
func (s *Scheduler) Insert(wo WorkOrder, strict bool, now int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validWorkOrder(wo) || !validNow(now) {
		return Result{}, ErrInvalidArgument
	}
	if now < s.nowMax {
		return Result{}, ErrClockRollback
	}
	if s.ln.Has(wo.ID) {
		return Result{}, ErrDuplicate
	}

	trial := s.ln.Clone()
	trial.AdvanceFrozen(now + s.f)
	earliest := wo.Ready
	if cand := now + s.f; cand > earliest {
		earliest = cand
	}
	job, pos := trial.Insert(wo.ID, wo.Family, wo.Duration, wo.Due, wo.Ready, earliest)
	if strict {
		if job.End > wo.Due {
			return Result{}, ErrDueConflict
		}
		after := trial.List()
		for i := pos + 1; i < trial.Len(); i++ {
			moved := after[i]
			before, exists := s.ln.Get(moved.ID)
			if exists && before.End <= before.Due && moved.End > moved.Due {
				return Result{}, ErrDueConflict
			}
		}
	}

	s.ln = trial
	s.nowMax = now
	return s.resultOf(job.ID), nil
}

// Remove 移除非冻结工单并重算其后工单；工单已冻结则拒绝。
func (s *Scheduler) Remove(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validNow(now) {
		return ErrInvalidArgument
	}
	if now < s.nowMax {
		return ErrClockRollback
	}

	trial := s.ln.Clone()
	trial.AdvanceFrozen(now + s.f)
	pos := trial.Index(id)
	if pos < 0 {
		return ErrNotFound
	}
	if trial.IsFrozenIndex(pos) {
		return ErrFrozen
	}
	trial.RemoveAt(pos)

	s.ln = trial
	s.nowMax = now
	return nil
}

// Get 只读返回工单时刻与冻结状态，不推进时钟与冻结。
func (s *Scheduler) Get(id string) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !validID(id) {
		return Result{}, ErrInvalidArgument
	}
	if !s.ln.Has(id) {
		return Result{}, ErrNotFound
	}
	return s.resultOf(id), nil
}

// List 按序列次序只读列出全部工单。
func (s *Scheduler) List() []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := s.ln.List()
	out := make([]Result, len(jobs))
	for i, job := range jobs {
		out[i] = s.resultOfJob(job)
	}
	return out
}

// RecalcCount 暴露最近一次被接受的 Insert/Remove 实际重算的工单数。
func (s *Scheduler) RecalcCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ln.RecalcCount()
}

func (s *Scheduler) resultOf(id string) Result {
	job, _ := s.ln.Get(id)
	return s.resultOfJob(job)
}

func (s *Scheduler) resultOfJob(job line.Job) Result {
	pos := s.ln.Index(job.ID)
	return Result{
		ID:     job.ID,
		Start:  job.Start,
		End:    job.End,
		Late:   job.End > job.Due,
		Frozen: pos >= 0 && pos < s.ln.Frozen(),
	}
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

func validID(id string) bool { return len(id) >= 1 && len(id) <= 32 }

func validWorkOrder(wo WorkOrder) bool {
	return validID(wo.ID) && len(wo.Family) >= 1 && len(wo.Family) <= 32 &&
		wo.Duration >= 1 && wo.Duration <= 100_000 &&
		wo.Due >= 0 && wo.Due <= 1_000_000_000 &&
		wo.Ready >= 0 && wo.Ready <= 1_000_000_000
}
