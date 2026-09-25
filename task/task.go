// Package task 定义调度任务与全局序号发放器。
package task

// Task 是一个租户提交的调度单元。
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64 // 全局限号：并发提交时按入队锁串行化顺序发放
}

// Source 发放单调递增的全局限号。
type Source struct{ next int64 }

// NewSource 创建起始序号为 start（含）的序号源。
func NewSource(start int64) *Source { return &Source{next: start} }

// Issue 生成指定租户与代价的下一个任务。
func (s *Source) Issue(tenant string, cost float64) Task {
	t := Task{Tenant: tenant, Cost: cost, Seq: s.next}
	s.next++
	return t
}

// Last 返回下一个将发放的序号。
func (s *Source) Last() int64 { return s.next }
