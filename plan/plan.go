// Package plan 在 line 之上实现插单、冻结与交期承诺。
package plan

import (
	"errors"
	"sync"

	"ontology/line"
	"ontology/matrix"
)

var (
	ErrArgument      = matrix.ErrArgument
	ErrState         = matrix.ErrState
	ErrClockRollback = errors.New("plan: clock rollback")
	ErrDuplicate     = errors.New("plan: duplicate work order")
	ErrNotFound      = errors.New("plan: work order not found")
	ErrFrozen        = errors.New("plan: work order frozen")
	ErrDueConflict   = errors.New("plan: due date conflict")
)

const (
	maxF       = 1_000_000
	maxNow     = 1_000_000_000
	maxDur     = 100_000
	nameMaxLen = 32
)

// WorkOrder 是插单请求。
type WorkOrder struct {
	ID       string
	Family   string
	Duration int
	Due      int
	Ready    int
}

// InsertResult 是 Insert 的返回。
type InsertResult struct {
	Start   int
	End     int
	Delayed bool
}

// Entry 是 List/Get 的只读视图。
type Entry struct {
	ID       string
	Family   string
	Duration int
	Due      int
	Ready    int
	Start    int
	End      int
	Frozen   bool
}

// Scheduler 是产线工单排产器。
type Scheduler struct {
	mu sync.RWMutex

	f  int
	mt matrix.Matrix
	ln *line.Line

	now    int
	hasNow bool
	frozen int // 冻结前缀长度
	seq    int // 已接受 Insert 计数（接受序号）

	// recalc 记录最近一次被接受 Insert/Remove 的重算工单数。
	recalc int
}

// New 构造排产器。f 为冻结提前量，t0 为产线可用时刻，f0 为初始产品族。
func New(f, t0 int, f0 string) (*Scheduler, error) {
	if f < 0 || f > maxF || t0 < 0 || !validName(f0) {
		return nil, ErrArgument
	}
	s := &Scheduler{f: f}
	s.ln = line.New(t0, f0, s.mt.Changeover)
	return s, nil
}

// SetChangeover 设定换型时长，仅计划序列为空时允许，否则返回 ErrState。
func (s *Scheduler) SetChangeover(a, b string, minutes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !matrix.ValidChangeover(a, b, minutes) {
		return ErrArgument
	}
	if s.ln.Len() > 0 {
		return ErrState
	}
	return s.mt.SetChangeover(a, b, minutes)
}

// Insert 按排序规则插单；strict 为真时对新增延期返回 ErrDueConflict。
func (s *Scheduler) Insert(wo WorkOrder, strict bool, now int) (InsertResult, error) {
	if !validWO(wo) || now < 0 || now > maxNow {
		return InsertResult{}, ErrArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasNow && now < s.now {
		return InsertResult{}, ErrClockRollback
	}
	if s.ln.IndexOf(wo.ID) >= 0 {
		return InsertResult{}, ErrDuplicate
	}

	// 先在克隆序列上推进冻结并试算，失败时原计划逐字段不变。
	trial := s.ln.Clone()
	newFrozen := advanceFrozen(trial, s.frozen, now+s.f)

	seq := s.seq + 1
	earliest := now + s.f
	if wo.Ready > earliest {
		earliest = wo.Ready
	}
	ord := &line.Order{
		ID:       wo.ID,
		Family:   wo.Family,
		Duration: wo.Duration,
		Due:      wo.Due,
		Ready:    wo.Ready,
		Seq:      seq,
		Earliest: earliest,
	}

	pos := newFrozen
	for pos < trial.Len() && trial.At(pos).Due <= wo.Due {
		pos++
	}

	trial.InsertAt(pos, ord)
	n := trial.RecalcFrom(pos)

	if strict {
		if ord.End > ord.Due {
			return InsertResult{}, ErrDueConflict
		}
		// 位置 pos 之后的工单：插入前 end≤due 而插入后 end>due 即新增延期。
		for i := pos + 1; i < trial.Len(); i++ {
			no := trial.At(i)
			old := s.ln.At(i - 1)
			if old.End <= old.Due && no.End > no.Due {
				return InsertResult{}, ErrDueConflict
			}
		}
	}

	// 接受：提交时钟、冻结、序列与计数。
	s.commit(now, newFrozen, seq, trial, n)
	return InsertResult{Start: ord.Start, End: ord.End, Delayed: ord.End > ord.Due}, nil
}

// Remove 移除非冻结工单并重算其后工单。
func (s *Scheduler) Remove(id string, now int) error {
	if !validName(id) || now < 0 || now > maxNow {
		return ErrArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasNow && now < s.now {
		return ErrClockRollback
	}
	idx := s.ln.IndexOf(id)
	if idx < 0 {
		return ErrNotFound
	}

	newFrozen := advanceFrozen(s.ln, s.frozen, now+s.f)
	if idx < newFrozen {
		return ErrFrozen
	}

	s.ln.RemoveAt(idx)
	n := s.ln.RecalcFrom(idx)
	s.commit(now, newFrozen, s.seq, s.ln, n)
	return nil
}

// Get 只读返回工单时刻与冻结状态，不推进冻结。
func (s *Scheduler) Get(id string) (Entry, error) {
	if !validName(id) {
		return Entry{}, ErrArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx := s.ln.IndexOf(id)
	if idx < 0 {
		return Entry{}, ErrNotFound
	}
	return s.toEntry(s.ln.At(idx), idx), nil
}

// List 按序列次序列出全部工单，不推进冻结。
func (s *Scheduler) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.ln.All()
	out := make([]Entry, len(all))
	for i, o := range all {
		out[i] = s.toEntry(o, i)
	}
	return out
}

// recalcCount 返回最近一次被接受操作的重算工单数，供测试校验。
func (s *Scheduler) recalcCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recalc
}

func (s *Scheduler) commit(now, newFrozen, seq int, ln *line.Line, n int) {
	s.now = now
	s.hasNow = true
	s.frozen = newFrozen
	s.seq = seq
	s.ln = ln
	s.recalc = n
}

func (s *Scheduler) toEntry(o *line.Order, idx int) Entry {
	return Entry{
		ID:       o.ID,
		Family:   o.Family,
		Duration: o.Duration,
		Due:      o.Due,
		Ready:    o.Ready,
		Start:    o.Start,
		End:      o.End,
		Frozen:   idx < s.frozen,
	}
}

// advanceFrozen 在给定序列上，从已有冻结前缀之后继续扩展：
// start < boundary 的最长前缀冻结；start 恰等 boundary 不冻结。
func advanceFrozen(ln *line.Line, cur, boundary int) int {
	for cur < ln.Len() && ln.At(cur).Start < boundary {
		cur++
	}
	return cur
}

func validName(s string) bool {
	n := len(s)
	return n >= 1 && n <= nameMaxLen
}

func validWO(wo WorkOrder) bool {
	return validName(wo.ID) && validName(wo.Family) &&
		wo.Duration >= 1 && wo.Duration <= maxDur &&
		wo.Due >= 0 && wo.Due <= maxNow &&
		wo.Ready >= 0 && wo.Ready <= maxNow
}
