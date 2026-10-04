// Package line 负责有序工单序列的维护与开工/完工时刻递推。
package line

import "sort"

// Job 是序列中的一张工单；接受后除 start/end 外字段均不变。
type Job struct {
	ID       string
	Family   string
	Duration int64
	Due      int64
	Ready    int64
	Earliest int64
	Order    uint64
	Start    int64
	End      int64
}

// ChangeoverFunc 返回从族 a 换到族 b 的换型时长。
type ChangeoverFunc func(a, b string) int64

// Line 是一条产线的有序工单序列。
type Line struct {
	t0     int64
	f0     string
	co     ChangeoverFunc
	jobs   []*Job
	index  map[string]int
	frozen int
	recalc int
	next   uint64
}

// New 创建产线序列。
func New(t0 int64, f0 string, co ChangeoverFunc) *Line {
	return &Line{t0: t0, f0: f0, co: co, index: map[string]int{}}
}

// Len 返回工单总数。
func (l *Line) Len() int { return len(l.jobs) }

// Frozen 返回当前冻结前缀长度。
func (l *Line) Frozen() int { return l.frozen }

// Empty 判断序列是否为空。
func (l *Line) Empty() bool { return len(l.jobs) == 0 }

// Has 判断工单号是否存在。
func (l *Line) Has(id string) bool { _, ok := l.index[id]; return ok }

// Index 返回工单号在序列中的下标，不存在时返回 -1。
func (l *Line) Index(id string) int {
	if pos, ok := l.index[id]; ok {
		return pos
	}
	return -1
}

// IsFrozenIndex 判断下标处工单是否已冻结。
func (l *Line) IsFrozenIndex(pos int) bool { return pos < l.frozen }

// Insert 按 (due 升序, 接受序号升序) 将新单插入非冻结段，并从插入点起重算。
// 返回新单及其插入下标。earliest 在接受时由调用方固定后传入。
func (l *Line) Insert(id, family string, duration, due, ready, earliest int64) (*Job, int) {
	job := &Job{
		ID:       id,
		Family:   family,
		Duration: duration,
		Due:      due,
		Ready:    ready,
		Earliest: earliest,
		Order:    l.next,
	}
	l.next++
	tail := l.jobs[l.frozen:]
	pos := l.frozen + sort.Search(len(tail), func(k int) bool { return tail[k].Due > due })
	l.jobs = append(l.jobs, nil)
	copy(l.jobs[pos+1:], l.jobs[pos:])
	l.jobs[pos] = job
	l.reindex(pos)
	l.recalc = 0
	l.recomputeFrom(pos)
	return job, pos
}

// RemoveAt 移除指定下标工单，收缩索引并从该点起按新前驱重算。
func (l *Line) RemoveAt(pos int) {
	id := l.jobs[pos].ID
	l.jobs = append(l.jobs[:pos], l.jobs[pos+1:]...)
	delete(l.index, id)
	l.reindex(pos)
	l.recalc = 0
	l.recomputeFrom(pos)
}

// AdvanceFrozen 以冻结时刻上界 limit 推进冻结前缀：start < limit 才冻结，
// start 恰等于 limit 不冻结。
func (l *Line) AdvanceFrozen(limit int64) {
	for l.frozen < len(l.jobs) && l.jobs[l.frozen].Start < limit {
		l.frozen++
	}
}

// Get 返回工单只读副本。
func (l *Line) Get(id string) (Job, bool) {
	pos, ok := l.index[id]
	if !ok {
		return Job{}, false
	}
	return *l.jobs[pos], true
}

// IsFrozen 判断指定工单是否已冻结。
func (l *Line) IsFrozen(id string) bool {
	pos, ok := l.index[id]
	return ok && pos < l.frozen
}

// List 按序列次序返回全部工单的只读副本。
func (l *Line) List() []Job {
	out := make([]Job, len(l.jobs))
	for i, job := range l.jobs {
		out[i] = *job
	}
	return out
}

// RecalcCount 返回最近一次 Insert/Remove 实际重算的工单数。
func (l *Line) RecalcCount() int { return l.recalc }

// Clone 返回整条序列的深拷贝，供严格插单试算与失败回滚。
func (l *Line) Clone() *Line {
	cp := &Line{
		t0:     l.t0,
		f0:     l.f0,
		co:     l.co,
		jobs:   make([]*Job, len(l.jobs)),
		index:  make(map[string]int, len(l.jobs)),
		frozen: l.frozen,
		recalc: l.recalc,
		next:   l.next,
	}
	for i, job := range l.jobs {
		jobCopy := *job
		cp.jobs[i] = &jobCopy
		cp.index[job.ID] = i
	}
	return cp
}

// recomputeFrom 从 pos 起按前驱完工时刻与换型递推 start/end；
// pos 之前的工单（含全部冻结工单）一律不触碰。
func (l *Line) recomputeFrom(pos int) {
	var prevEnd int64 = l.t0
	prevFamily := l.f0
	if pos > 0 {
		prev := l.jobs[pos-1]
		prevEnd = prev.End
		prevFamily = prev.Family
	}
	for i := pos; i < len(l.jobs); i++ {
		job := l.jobs[i]
		start := prevEnd + l.co(prevFamily, job.Family)
		if job.Earliest > start {
			start = job.Earliest
		}
		job.Start = start
		job.End = start + job.Duration
		prevEnd = job.End
		prevFamily = job.Family
		l.recalc++
	}
}

// reindex 从 from 起重建工单号到下标的索引。
func (l *Line) reindex(from int) {
	for i := from; i < len(l.jobs); i++ {
		l.index[l.jobs[i].ID] = i
	}
}
