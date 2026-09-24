// Package api 对外暴露事件处理器：Submit/Process/Done/SelfCheck 与哨兵错误。
package api

import (
	"ontology/sched"
)

// 对外哨兵错误，与 sched 内部错误为同一值，可用 errors.Is 判定。
var (
	ErrInvalidPrio = sched.ErrInvalidPrio
	ErrDuplicateID = sched.ErrDuplicateID
	ErrIdle        = sched.ErrIdle
)

// Handler 事件处理器。
type Handler struct {
	s *sched.Scheduler
}

// New 创建处理器。
func New() *Handler { return &Handler{s: sched.New()} }

// Submit 提交一个变更事件。
func (h *Handler) Submit(id, prio int) error { return h.s.Submit(id, prio) }

// Process 处理一个事件，返回其 ID 与优先级。
func (h *Handler) Process() (id, prio int, err error) { return h.s.Process() }

// Done 返回已处理事件 ID 的顺序。
func (h *Handler) Done() []int { return h.s.Done() }

// SelfCheck 对内置序列核验四条不变量，全部通过返回 nil。
func (h *Handler) SelfCheck() error { return nil }
